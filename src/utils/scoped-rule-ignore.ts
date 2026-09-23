import {getPositions, MDAstTypes} from './mdast';
import {escapeRegExp, yamlRegex} from './regex';
import {replaceTextBetweenStartAndEndWithNewValue} from './strings';

const knownRuleAliases = new Set<string>();

const HTML_MARKER_LINE = /^[ \t]*<!--[ \t]*linter-(disable-next-n-lines|disable-next-line|disable|enable)\b([\s\S]*?)[ \t]*-->[ \t]*$/;
const OBSIDIAN_MARKER_LINE = /^[ \t]*%%[ \t]*linter-(disable-next-n-lines|disable-next-line|disable|enable)\b([\s\S]*?)[ \t]*%%[ \t]*$/;

type TextRange = {startIndex: number, endIndex: number};

type TextLine = {
  text: string,
  ending: string,
  start: number,
};

type Scope = {
  all: boolean,
  rules: Set<string>,
  exceptions: Set<string>,
};

type LineDisable = {
  remaining: number,
  all: boolean,
  rules: Set<string>,
};

type LineInfo = {
  isMarker: boolean,
  lineAll: boolean,
  lineRules: Set<string>,
  allScopes: {exceptions: Set<string>}[],
  specificRules: Set<string>,
};

type RuleList = {kind: 'all'} | {kind: 'rules', rules: Set<string>} | {kind: 'none'};

type ParsedMarker = {
  action: 'disable' | 'enable' | 'next' | 'none',
  count?: number,
  rules?: RuleList,
};

/**
 * Records a rule alias that scoped ignore markers are allowed to name.
 * @param {string} alias - Rule alias to recognize in marker rule lists
 */
export function registerScopedIgnoreRuleAlias(alias: string): void {
  if (!alias) {
    return;
  }

  knownRuleAliases.add(alias.toLowerCase());
}

/**
 * Runs a transform while protecting lines this rule must not change.
 * Marker lines are always protected. Lines disabled for the rule are protected too.
 * @param {string} text - Original note text
 * @param {string} ruleAlias - Alias of the rule about to run
 * @param {function(string): string} func - Transform to run against the masked text
 * @return {string} Text after the transform, with protected lines restored
 */
export function withScopedRuleIgnore(text: string, ruleAlias: string, func: (text: string) => string): string {
  const ranges = collectProtectedRanges(text, ruleAlias);
  if (ranges.length === 0) {
    return func(text);
  }

  const originals: string[] = [];
  let masked = text;
  for (let index = ranges.length - 1; index >= 0; index--) {
    const range = ranges[index];
    originals[index] = text.substring(range.startIndex, range.endIndex);
    masked = replaceTextBetweenStartAndEndWithNewValue(masked, range.startIndex, range.endIndex, placeholderFor(index));
  }

  return restorePlaceholders(func(masked), originals);
}

/**
 * Returns ignore ranges that hide marker lines and all-rules disabled lines.
 * Ranges are last-to-first so callers can replace them without shifting indexes.
 * Each endIndex is exclusive and does not include the final protected line's line ending.
 * @param {string} text - Note text to scan
 * @return {TextRange[]} Protected ranges from the end of the note toward the start
 */
export function getAllCustomIgnoreSectionsInText(text: string): TextRange[] {
  return collectProtectedRanges(text, null).reverse();
}

function placeholderFor(index: number): string {
  return `{SCOPED_RULE_IGNORE_${index}}`;
}

function restorePlaceholders(text: string, originals: string[]): string {
  let restored = text;
  for (let index = originals.length - 1; index >= 0; index--) {
    const pattern = new RegExp(escapeRegExp(placeholderFor(index)), 'i');
    restored = restored.replace(pattern, () => originals[index]);
  }

  return restored;
}

function collectProtectedRanges(text: string, ruleAlias: string | null): TextRange[] {
  if (!text.includes('linter-disable') && !text.includes('linter-enable')) {
    return [];
  }

  const lines = splitLines(text);
  const infos = analyzeLines(text, lines);
  const ranges: TextRange[] = [];
  let index = 0;
  while (index < lines.length) {
    if (!isLineProtected(infos[index], ruleAlias)) {
      index++;
      continue;
    }

    const startIndex = lines[index].start;
    let endIndex = lines[index].start + lines[index].text.length;
    index++;
    while (index < lines.length && isLineProtected(infos[index], ruleAlias)) {
      endIndex = lines[index].start + lines[index].text.length;
      index++;
    }

    ranges.push({startIndex, endIndex});
  }

  return ranges;
}

function isLineProtected(info: LineInfo, ruleAlias: string | null): boolean {
  if (info.isMarker) {
    return true;
  }

  if (ruleAlias === null) {
    return info.lineAll || info.allScopes.length > 0;
  }

  return isRuleDisabled(info, ruleAlias);
}

function isRuleDisabled(info: LineInfo, ruleAlias: string): boolean {
  const alias = ruleAlias.toLowerCase();
  if (info.lineAll || info.lineRules.has(alias)) {
    return true;
  }

  for (const scope of info.allScopes) {
    if (!scope.exceptions.has(alias)) {
      return true;
    }
  }

  return info.specificRules.has(alias);
}

function analyzeLines(text: string, lines: TextLine[]): LineInfo[] {
  const blindRanges = getBlindRanges(text);
  const infos: LineInfo[] = [];
  const stack: Scope[] = [];
  let pending: LineDisable[] = [];

  for (const line of lines) {
    const activeCount = pending.length;
    const marker = lineIsBlind(line, blindRanges) ? null : parseMarker(line.text);
    if (marker) {
      infos.push(markerInfo());
      applyMarker(marker, stack, pending);
    } else {
      infos.push(snapshotContent(stack, pending, activeCount));
    }

    pending = consumeExisting(pending, activeCount);
  }

  return infos;
}

function splitLines(text: string): TextLine[] {
  const lines: TextLine[] = [];
  const endingRegex = /\r\n|\n|\r/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = endingRegex.exec(text)) !== null) {
    lines.push({
      text: text.slice(lastIndex, match.index),
      ending: match[0],
      start: lastIndex,
    });
    lastIndex = match.index + match[0].length;
  }

  if (lastIndex < text.length) {
    lines.push({
      text: text.slice(lastIndex),
      ending: '',
      start: lastIndex,
    });
  }

  return lines;
}

function getBlindRanges(text: string): TextRange[] {
  const ranges: TextRange[] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch && yamlMatch.index !== undefined) {
    ranges.push({
      startIndex: yamlMatch.index,
      endIndex: yamlMatch.index + yamlMatch[0].length,
    });
  }

  const types = [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math, MDAstTypes.InlineMath];
  for (const type of types) {
    const positions = getPositions(type, text);
    for (const position of positions) {
      const start = position?.start?.offset;
      const end = position?.end?.offset;
      if (typeof start === 'number' && typeof end === 'number') {
        ranges.push({startIndex: start, endIndex: end});
      }
    }
  }

  // HTML blocks interrupt CommonMark inline code, so a marker on its own line between
  // backticks is not an inlineCode node. Pair backtick runs directly so those markers stay inert.
  // Backticks already inside YAML, code, math, or parsed inline code are masked first.
  for (const range of findBacktickCodeRanges(text, ranges)) {
    ranges.push(range);
  }

  return ranges;
}

function findBacktickCodeRanges(text: string, blocked: TextRange[]): TextRange[] {
  const scan = maskBackticks(text, blocked);
  const ranges: TextRange[] = [];
  let index = 0;
  while (index < scan.length) {
    if (scan.charAt(index) !== '`') {
      index++;
      continue;
    }

    const start = index;
    let ticks = 0;
    while (index < scan.length && scan.charAt(index) === '`') {
      ticks++;
      index++;
    }

    const closer = findBacktickCloser(scan, index, ticks);
    if (closer === -1) {
      continue;
    }

    ranges.push({startIndex: start, endIndex: closer + ticks});
    index = closer + ticks;
  }

  return ranges;
}

function maskBackticks(text: string, blocked: TextRange[]): string {
  if (blocked.length === 0 || !text.includes('`')) {
    return text;
  }

  const chars = text.split('');
  for (const range of blocked) {
    const start = Math.max(0, range.startIndex);
    const end = Math.min(chars.length, range.endIndex);
    for (let index = start; index < end; index++) {
      if (chars[index] === '`') {
        chars[index] = ' ';
      }
    }
  }

  return chars.join('');
}

function findBacktickCloser(text: string, from: number, ticks: number): number {
  let index = from;
  while (index < text.length) {
    if (text.charAt(index) !== '`') {
      index++;
      continue;
    }

    const runStart = index;
    let count = 0;
    while (index < text.length && text.charAt(index) === '`') {
      count++;
      index++;
    }

    if (count === ticks) {
      return runStart;
    }
  }

  return -1;
}

function lineIsBlind(line: TextLine, ranges: TextRange[]): boolean {
  const start = line.start;
  const end = line.start + line.text.length;
  if (end <= start) {
    return false;
  }

  for (const range of ranges) {
    if (start < range.endIndex && range.startIndex < end) {
      return true;
    }
  }

  return false;
}

function markerInfo(): LineInfo {
  return {
    isMarker: true,
    lineAll: false,
    lineRules: new Set<string>(),
    allScopes: [],
    specificRules: new Set<string>(),
  };
}

function snapshotContent(stack: Scope[], pending: LineDisable[], activeCount: number): LineInfo {
  const allScopes: {exceptions: Set<string>}[] = [];
  const specificRules = new Set<string>();
  for (const scope of stack) {
    if (scope.all) {
      allScopes.push({exceptions: copySet(scope.exceptions)});
    } else {
      scope.rules.forEach((rule) => specificRules.add(rule));
    }
  }

  let lineAll = false;
  const lineRules = new Set<string>();
  for (let index = 0; index < activeCount; index++) {
    const item = pending[index];
    if (item.all) {
      lineAll = true;
    }
    item.rules.forEach((rule) => lineRules.add(rule));
  }

  return {
    isMarker: false,
    lineAll,
    lineRules,
    allScopes,
    specificRules,
  };
}

function copySet(source: Set<string>): Set<string> {
  const copy = new Set<string>();
  source.forEach((value) => copy.add(value));
  return copy;
}

function consumeExisting(pending: LineDisable[], activeCount: number): LineDisable[] {
  const next: LineDisable[] = [];
  for (let index = 0; index < pending.length; index++) {
    const item = pending[index];
    if (index < activeCount) {
      item.remaining -= 1;
    }
    if (item.remaining > 0) {
      next.push(item);
    }
  }

  return next;
}

function applyMarker(marker: ParsedMarker, stack: Scope[], pending: LineDisable[]): void {
  if (marker.action === 'none' || !marker.rules || marker.rules.kind === 'none') {
    return;
  }

  if (marker.action === 'disable') {
    if (marker.rules.kind === 'all') {
      stack.push({all: true, rules: new Set<string>(), exceptions: new Set<string>()});
    } else {
      stack.push({all: false, rules: marker.rules.rules, exceptions: new Set<string>()});
    }
    return;
  }

  if (marker.action === 'enable') {
    if (marker.rules.kind === 'all') {
      if (stack.length > 0) {
        stack.pop();
      }
      return;
    }

    enableListedRules(stack, marker.rules.rules);
    return;
  }

  pending.push({
    remaining: marker.count ?? 0,
    all: marker.rules.kind === 'all',
    rules: marker.rules.kind === 'rules' ? copySet(marker.rules.rules) : new Set<string>(),
  });
}

function enableListedRules(stack: Scope[], rules: Set<string>): void {
  const names: string[] = [];
  rules.forEach((rule) => names.push(rule));
  for (const rule of names) {
    for (let index = stack.length - 1; index >= 0; index--) {
      if (!scopeDisables(stack[index], rule)) {
        continue;
      }

      stopDisabling(stack[index], rule);
      if (scopeIsEmpty(stack[index])) {
        stack.splice(index, 1);
      }
      break;
    }
  }
}

function scopeDisables(scope: Scope, rule: string): boolean {
  if (scope.all) {
    return !scope.exceptions.has(rule);
  }

  return scope.rules.has(rule);
}

function stopDisabling(scope: Scope, rule: string): void {
  if (scope.all) {
    scope.exceptions.add(rule);
    return;
  }

  scope.rules.delete(rule);
}

function scopeIsEmpty(scope: Scope): boolean {
  return !scope.all && scope.rules.size === 0;
}

function parseMarker(line: string): ParsedMarker | null {
  const match = HTML_MARKER_LINE.exec(line) ?? OBSIDIAN_MARKER_LINE.exec(line);
  if (!match) {
    return null;
  }

  const keyword = match[1];
  const rest = match[2] ?? '';
  if (keyword === 'disable') {
    return {action: 'disable', rules: normalizeRuleList(rest)};
  }
  if (keyword === 'enable') {
    return {action: 'enable', rules: normalizeRuleList(rest)};
  }
  if (keyword === 'disable-next-line') {
    return {action: 'next', count: 1, rules: normalizeRuleList(rest)};
  }

  const parsedCount = parseNextNLines(rest);
  if (!parsedCount) {
    return {action: 'none'};
  }

  return {action: 'next', count: parsedCount.count, rules: normalizeRuleList(parsedCount.ruleText)};
}

function parseNextNLines(raw: string): {count: number, ruleText: string} | null {
  const match = raw.match(/^\s*:\s*([0-9]+)([\s\S]*)$/);
  if (!match) {
    return null;
  }

  const digits = match[1];
  const after = match[2];
  if (after.length > 0 && !/^[\s,]/.test(after)) {
    return null;
  }

  const count = parseInt(digits, 10);
  if (!Number.isSafeInteger(count) || count <= 0) {
    if (/^[1-9][0-9]+$/.test(digits)) {
      return {count: Number.MAX_SAFE_INTEGER, ruleText: after};
    }

    return null;
  }

  return {count, ruleText: after};
}

function normalizeRuleList(raw: string): RuleList {
  if (raw.trim() === '') {
    return {kind: 'all'};
  }

  const rules = new Set<string>();
  const parts = raw.split(',');
  for (const part of parts) {
    const alias = part.trim().toLowerCase();
    if (alias === '' || !knownRuleAliases.has(alias)) {
      continue;
    }

    rules.add(alias);
  }

  if (rules.size === 0) {
    return {kind: 'none'};
  }

  return {kind: 'rules', rules};
}
