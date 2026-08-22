import {yamlRegex} from './regex';
import {getPositions, MDAstTypes} from './mdast';

export type LinterIgnoreCommand =
  | 'linter-disable'
  | 'linter-enable'
  | 'linter-disable-next-line'
  | 'linter-disable-next-n-lines';

export type ParsedLinterIgnoreMarker = {
  lineIndex: number,
  startOffset: number,
  endOffset: number,
  command: LinterIgnoreCommand,
  allRules: boolean,
  aliases: string[],
  nextLineCount: number | null,
};

type LineInfo = {
  content: string,
  start: number,
  end: number,
};

type DisableScope =
  | {kind: 'all', except: Set<string>}
  | {kind: 'rules', aliases: Set<string>};

type LineScopedDisable = {
  all: boolean,
  aliases: Set<string>,
};

const STANDALONE_MARKER_REGEX = /^[ \t]*(?:<!--[ \t]*(linter-disable-next-n-lines:([ \t]*\S+)|linter-disable-next-line|linter-disable|linter-enable)[ \t]*([\s\S]*?)[ \t]*-->|%%[ \t]*(linter-disable-next-n-lines:([ \t]*\S+)|linter-disable-next-line|linter-disable|linter-enable)[ \t]*([\s\S]*?)[ \t]*%%)[ \t]*$/;

let knownRuleAliasProvider: () => ReadonlySet<string> = () => new Set();

export function setKnownRuleAliasProvider(provider: () => ReadonlySet<string>): void {
  knownRuleAliasProvider = provider;
}

export function getKnownRuleAliases(): ReadonlySet<string> {
  return knownRuleAliasProvider();
}

export function getAllCustomIgnoreSectionsInText(text: string, ruleAlias?: string, knownAliases: ReadonlySet<string> = getKnownRuleAliases()): {startIndex: number, endIndex: number}[] {
  return getIgnoreRangesForRule(text, ruleAlias, knownAliases);
}

export function normalizeRuleAliasList(rawList: string, knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>): string[] {
  const seen = new Set<string>();
  const normalized: string[] = [];

  for (const part of rawList.split(',')) {
    const alias = part.trim().toLowerCase();
    if (alias === '') {
      continue;
    }

    const canonical = resolveKnownAlias(alias, knownAliases);
    if (canonical === null || seen.has(canonical)) {
      continue;
    }

    seen.add(canonical);
    normalized.push(canonical);
  }

  return normalized;
}

export function getIgnoreRangesForRule(text: string, ruleAlias: string | undefined, knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>): {startIndex: number, endIndex: number}[] {
  const lines = splitLines(text);
  if (lines.length === 0) {
    return [];
  }

  const protectedRanges = getProtectedRanges(text);
  const markersByLine = new Map<number, ParsedLinterIgnoreMarker>();
  for (const marker of parseStandaloneMarkers(lines, protectedRanges, knownAliases)) {
    markersByLine.set(marker.lineIndex, marker);
  }

  const ignoredLines = new Array<boolean>(lines.length).fill(false);
  const stack: DisableScope[] = [];
  const lineScoped = new Map<number, LineScopedDisable>();

  for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
    const marker = markersByLine.get(lineIndex);
    if (marker) {
      ignoredLines[lineIndex] = true;
      applyMarker(stack, lineScoped, marker, lines.length);
      continue;
    }

    if (isLineDisabled(stack, lineScoped.get(lineIndex), ruleAlias)) {
      ignoredLines[lineIndex] = true;
    }
  }

  return lineFlagsToRanges(lines, ignoredLines);
}

function applyMarker(stack: DisableScope[], lineScoped: Map<number, LineScopedDisable>, marker: ParsedLinterIgnoreMarker, lineCount: number): void {
  if (marker.command === 'linter-enable') {
    applyEnable(stack, marker);
    return;
  }

  if (marker.command === 'linter-disable') {
    if (marker.allRules) {
      stack.push({kind: 'all', except: new Set()});
    } else if (marker.aliases.length > 0) {
      stack.push({kind: 'rules', aliases: new Set(marker.aliases)});
    }
    return;
  }

  if (marker.nextLineCount === null || marker.nextLineCount < 1 || marker.lineIndex + 1 >= lineCount) {
    return;
  }

  const lastLine = Math.min(marker.lineIndex + marker.nextLineCount, lineCount - 1);
  for (let lineIndex = marker.lineIndex + 1; lineIndex <= lastLine; lineIndex++) {
    addLineScopedDisable(lineScoped, lineIndex, marker);
  }
}

function resolveKnownAlias(alias: string, knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>): string | null {
  if (knownAliases instanceof Map) {
    return knownAliases.get(alias) ?? null;
  }

  return knownAliases.has(alias) ? alias : null;
}

function parseStandaloneMarkers(lines: LineInfo[], protectedRanges: {start: number, end: number}[], knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>): ParsedLinterIgnoreMarker[] {
  const markers: ParsedLinterIgnoreMarker[] = [];

  for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
    const line = lines[lineIndex];
    if (isOffsetProtected(protectedRanges, line.start)) {
      continue;
    }

    const match = STANDALONE_MARKER_REGEX.exec(line.content);
    if (!match) {
      continue;
    }

    const rawCommand = match[1] ?? match[4];
    const rawNextN = match[2] ?? match[5];
    const rawList = match[3] ?? match[6] ?? '';
    const parsed = parseCommand(rawCommand, rawNextN, rawList, knownAliases);
    if (parsed === null) {
      continue;
    }

    markers.push({
      lineIndex,
      startOffset: line.start,
      endOffset: line.start + line.content.length,
      ...parsed,
    });
  }

  return markers;
}

function parseCommand(rawCommand: string, rawNextN: string | undefined, rawList: string, knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>): {
  command: LinterIgnoreCommand,
  allRules: boolean,
  aliases: string[],
  nextLineCount: number | null,
} | null {
  if (rawCommand.startsWith('linter-disable-next-n-lines:')) {
    const nextLineCount = parsePositiveInteger(rawNextN ?? '');
    const {allRules, aliases, hasEffect} = parseRuleList(rawList, knownAliases, true);
    if (!hasEffect || nextLineCount === null) {
      return {
        command: 'linter-disable-next-n-lines',
        allRules: false,
        aliases: [],
        nextLineCount,
      };
    }

    return {
      command: 'linter-disable-next-n-lines',
      allRules,
      aliases,
      nextLineCount,
    };
  }

  if (rawCommand === 'linter-disable-next-line') {
    const {allRules, aliases, hasEffect} = parseRuleList(rawList, knownAliases, true);
    if (!hasEffect) {
      return {
        command: 'linter-disable-next-line',
        allRules: false,
        aliases: [],
        nextLineCount: null,
      };
    }

    return {
      command: 'linter-disable-next-line',
      allRules,
      aliases,
      nextLineCount: 1,
    };
  }

  if (rawCommand === 'linter-disable') {
    const {allRules, aliases, hasEffect} = parseRuleList(rawList, knownAliases, true);
    if (!hasEffect) {
      return {
        command: 'linter-disable',
        allRules: false,
        aliases: [],
        nextLineCount: null,
      };
    }

    return {
      command: 'linter-disable',
      allRules,
      aliases,
      nextLineCount: null,
    };
  }

  if (rawCommand === 'linter-enable') {
    const {allRules, aliases, hasEffect} = parseRuleList(rawList, knownAliases, true);
    if (!hasEffect) {
      return {
        command: 'linter-enable',
        allRules: false,
        aliases: [],
        nextLineCount: null,
      };
    }

    return {
      command: 'linter-enable',
      allRules,
      aliases,
      nextLineCount: null,
    };
  }

  return null;
}

function parseRuleList(rawList: string, knownAliases: ReadonlySet<string> | ReadonlyMap<string, string>, emptyMeansAll: boolean): {allRules: boolean, aliases: string[], hasEffect: boolean} {
  if (rawList.trim() === '') {
    return {allRules: emptyMeansAll, aliases: [], hasEffect: true};
  }

  const aliases = normalizeRuleAliasList(rawList, knownAliases);
  if (aliases.length === 0) {
    return {allRules: false, aliases: [], hasEffect: false};
  }

  return {allRules: false, aliases, hasEffect: true};
}

function parsePositiveInteger(raw: string): number | null {
  const trimmed = raw.trim();
  if (!/^[1-9]\d*$/.test(trimmed)) {
    return null;
  }

  return Number.parseInt(trimmed, 10);
}

function applyEnable(stack: DisableScope[], marker: ParsedLinterIgnoreMarker): void {
  if (marker.allRules) {
    stack.pop();
    return;
  }

  for (const alias of marker.aliases) {
    for (let index = stack.length - 1; index >= 0; index--) {
      const scope = stack[index];
      if (scope.kind === 'all' && !scope.except.has(alias)) {
        scope.except.add(alias);
        break;
      }

      if (scope.kind === 'rules' && scope.aliases.has(alias)) {
        scope.aliases.delete(alias);
        if (scope.aliases.size === 0) {
          stack.splice(index, 1);
        }
        break;
      }
    }
  }
}

function addLineScopedDisable(lineScoped: Map<number, LineScopedDisable>, lineIndex: number, marker: ParsedLinterIgnoreMarker): void {
  const current = lineScoped.get(lineIndex) ?? {all: false, aliases: new Set<string>()};
  if (marker.allRules) {
    current.all = true;
  } else {
    for (const alias of marker.aliases) {
      current.aliases.add(alias);
    }
  }
  lineScoped.set(lineIndex, current);
}

function scopeDisablesRule(scope: DisableScope, ruleAlias: string): boolean {
  if (scope.kind === 'all') {
    return !scope.except.has(ruleAlias);
  }

  return scope.aliases.has(ruleAlias);
}

function isLineDisabled(stack: DisableScope[], lineDisable: LineScopedDisable | undefined, ruleAlias: string | undefined): boolean {
  if (lineDisable?.all) {
    return true;
  }

  if (ruleAlias === undefined) {
    return stack.some((scope) => scope.kind === 'all' && scope.except.size === 0);
  }

  if (lineDisable?.aliases.has(ruleAlias)) {
    return true;
  }

  return stack.some((scope) => scopeDisablesRule(scope, ruleAlias));
}

function lineFlagsToRanges(lines: LineInfo[], ignoredLines: boolean[]): {startIndex: number, endIndex: number}[] {
  const ranges: {startIndex: number, endIndex: number}[] = [];
  let rangeStart: number | null = null;

  for (let lineIndex = 0; lineIndex <= ignoredLines.length; lineIndex++) {
    const ignored = lineIndex < ignoredLines.length && ignoredLines[lineIndex];
    if (ignored && rangeStart === null) {
      rangeStart = lineIndex;
    } else if (!ignored && rangeStart !== null) {
      ranges.push(offsetsForLineSpan(lines, rangeStart, lineIndex - 1));
      rangeStart = null;
    }
  }

  return ranges.reverse();
}

function offsetsForLineSpan(lines: LineInfo[], startLine: number, endLine: number): {startIndex: number, endIndex: number} {
  const startIndex = lines[startLine].start;
  const last = lines[endLine];
  const endIndex = last.start + last.content.length;
  return {startIndex, endIndex};
}

function splitLines(text: string): LineInfo[] {
  const lines: LineInfo[] = [];
  let start = 0;

  for (let index = 0; index < text.length; index++) {
    if (text[index] === '\n') {
      lines.push({
        content: text.slice(start, index),
        start,
        end: index + 1,
      });
      start = index + 1;
    } else if (text[index] === '\r' && text[index + 1] === '\n') {
      lines.push({
        content: text.slice(start, index),
        start,
        end: index + 2,
      });
      start = index + 2;
      index++;
    }
  }

  if (start < text.length || (text.length === 0)) {
    lines.push({
      content: text.slice(start),
      start,
      end: text.length,
    });
  } else if (start === text.length && text.length > 0) {
    // File ends with a newline; no extra empty line is needed for ignore scanning.
  }

  return lines;
}

function getProtectedRanges(text: string): {start: number, end: number}[] {
  const ranges: {start: number, end: number}[] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch && yamlMatch.index !== undefined) {
    ranges.push({start: yamlMatch.index, end: yamlMatch.index + yamlMatch[0].length});
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math, MDAstTypes.InlineMath]) {
    for (const position of getPositions(type, text)) {
      if (position?.start?.offset === undefined || position?.end?.offset === undefined) {
        continue;
      }
      ranges.push({start: position.start.offset, end: position.end.offset});
    }
  }

  return ranges;
}

function isOffsetProtected(ranges: {start: number, end: number}[], offset: number): boolean {
  return ranges.some((range) => offset >= range.start && offset < range.end);
}
