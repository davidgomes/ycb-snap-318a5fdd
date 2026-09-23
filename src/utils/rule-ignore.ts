import {getPositions, MDAstTypes} from './mdast';
import {yamlRegex} from './regex';

const knownRuleAliases = new Map<string, string>();

/**
 * Records a rule alias so marker rule lists can be matched case-insensitively.
 * @param {string} alias - Canonical rule alias
 */
export function registerRuleAlias(alias: string): void {
  knownRuleAliases.set(alias.toLowerCase(), alias);
}

type MarkerKind = 'disable' | 'enable' | 'disable-next-line' | 'disable-next-n-lines';

type ParsedMarker = {
  kind: MarkerKind,
  /** null when the marker does not carry a rule list (disable-all / pop scope). */
  rules: string[] | null,
  /** Set for next-n-lines. null means the count was not a positive integer. */
  lineCount: number | null,
};

type DisableScope = {
  all: boolean,
  rules: Set<string>,
  exceptions: Set<string>,
};

const KEYWORD_PATTERN = /^(linter-disable-next-n-lines|linter-disable-next-line|linter-disable|linter-enable)\b/i;

/**
 * Parses a standalone marker line. Returns null when the line is not a marker.
 * An invalid line count still returns a marker so the line is left untouched.
 * @param {string} line - One line of the note, without its trailing newline
 * @return {ParsedMarker | null} The parsed marker, or null when the line is not one
 */
export function parseStandaloneMarker(line: string): ParsedMarker | null {
  const html = /^[ \t]*<!--([\s\S]*?)-->[ \t]*$/.exec(line);
  const obsidian = html ? null : /^[ \t]*%%([\s\S]*?)%%[ \t]*$/.exec(line);
  const match = html ?? obsidian;
  if (!match) {
    return null;
  }

  const inner = match[1].trim();
  const keywordMatch = KEYWORD_PATTERN.exec(inner);
  if (!keywordMatch) {
    return null;
  }

  const keyword = keywordMatch[1].toLowerCase();
  let rest = inner.slice(keywordMatch[1].length);
  if (rest.length > 0 && !/^[\s:]/.test(rest)) {
    return null;
  }

  let lineCount: number | null = null;
  if (keyword === 'linter-disable-next-n-lines') {
    const countMatch = /^[ \t]*:[ \t]*(\S*)([\s\S]*)$/.exec(rest);
    if (!countMatch) {
      return null;
    }
    lineCount = parsePositiveInteger(countMatch[1]);
    rest = countMatch[2] ?? '';
  }

  const rules = normalizeRuleList(rest);
  const kind: MarkerKind = keyword === 'linter-disable' ? 'disable' :
    keyword === 'linter-enable' ? 'enable' :
    keyword === 'linter-disable-next-line' ? 'disable-next-line' :
    'disable-next-n-lines';

  return {kind, rules, lineCount};
}

/**
 * @param {string} token - Candidate count token
 * @return {number | null} The positive integer value, or null when the token is not one
 */
function parsePositiveInteger(token: string): number | null {
  if (!/^[0-9]+$/.test(token)) {
    return null;
  }
  const value = Number(token);
  if (!Number.isSafeInteger(value) || value <= 0) {
    return null;
  }
  return value;
}

/**
 * @param {string} rest - Text after the marker keyword
 * @return {string[] | null} Null when no rule list was provided. An empty array means the list was present but nothing survived normalization.
 */
function normalizeRuleList(rest: string): string[] | null {
  if (rest.trim() === '') {
    return null;
  }

  const aliases: string[] = [];
  const seen = new Set<string>();
  for (const part of rest.split(',')) {
    const token = part.trim();
    if (token === '') {
      continue;
    }
    const canonical = knownRuleAliases.get(token.toLowerCase());
    if (!canonical || seen.has(canonical)) {
      continue;
    }
    seen.add(canonical);
    aliases.push(canonical);
  }
  return aliases;
}

/**
 * @param {number} start - Start offset
 * @param {number} end - End offset
 * @param {{start: number, end: number}[]} ranges - Protected ranges
 * @return {boolean} Whether the offsets overlap any protected range
 */
function rangesOverlap(start: number, end: number, ranges: {start: number, end: number}[]): boolean {
  for (const range of ranges) {
    if (start < range.end && end > range.start) {
      return true;
    }
  }
  return false;
}

/**
 * @param {string} text - Note text
 * @return {{start: number, end: number}[]} Offsets where markers must not be recognized
 */
function protectedRegions(text: string): {start: number, end: number}[] {
  const regions: {start: number, end: number}[] = [];
  const yaml = yamlRegex.exec(text);
  if (yaml && yaml.index === 0) {
    regions.push({start: 0, end: yaml.index + yaml[0].length});
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      if (position?.start?.offset == null || position?.end?.offset == null) {
        continue;
      }
      regions.push({start: position.start.offset, end: position.end.offset});
    }
  }

  return regions;
}

/**
 * @param {string} text - Note text
 * @return {{text: string, start: number, end: number}[]} Lines and their offsets, ignoring a trailing empty split
 */
function splitLines(text: string): {text: string, start: number, end: number}[] {
  const lines: {text: string, start: number, end: number}[] = [];
  let offset = 0;
  const raw = text.split('\n');
  const dropTrailingEmpty = text.endsWith('\n');
  const count = dropTrailingEmpty ? raw.length - 1 : raw.length;
  for (let i = 0; i < count; i++) {
    let line = raw[i];
    if (line.endsWith('\r')) {
      line = line.slice(0, -1);
    }
    const start = offset;
    const end = offset + line.length;
    lines.push({text: line, start, end});
    offset += raw[i].length + 1;
  }
  return lines;
}

/**
 * @param {DisableScope} scope - Open disable scope
 * @param {string | null} ruleAlias - Rule being linted, or null for non-rule edits
 * @return {boolean} Whether the scope disables that rule
 */
function scopeDisables(scope: DisableScope, ruleAlias: string | null): boolean {
  if (scope.all) {
    return ruleAlias == null || !scope.exceptions.has(ruleAlias);
  }
  return ruleAlias != null && scope.rules.has(ruleAlias);
}

/**
 * @param {DisableScope[]} scopes - Active scopes
 * @param {string | null} ruleAlias - Rule being linted, or null for non-rule edits
 * @return {boolean} Whether any scope disables the rule
 */
function isRuleDisabled(scopes: DisableScope[], ruleAlias: string | null): boolean {
  for (const scope of scopes) {
    if (scopeDisables(scope, ruleAlias)) {
      return true;
    }
  }
  return false;
}

/**
 * Re-enables each alias on the nearest open scope that currently disables it.
 * @param {DisableScope[]} scopes - Active scopes, mutated in place
 * @param {string[]} aliases - Canonical rule aliases to re-enable
 */
function enableRules(scopes: DisableScope[], aliases: string[]): void {
  for (const alias of aliases) {
    for (let i = scopes.length - 1; i >= 0; i--) {
      const scope = scopes[i];
      if (scope.all) {
        if (!scope.exceptions.has(alias)) {
          scope.exceptions.add(alias);
          break;
        }
        continue;
      }
      if (scope.rules.has(alias)) {
        scope.rules.delete(alias);
        if (scope.rules.size === 0) {
          scopes.splice(i, 1);
        }
        break;
      }
    }
  }
}

/**
 * @param {string[] | null} rules - Null for every rule, or the normalized alias list
 * @return {DisableScope | null} The scope to open, or null when the marker has no effect
 */
function makeScope(rules: string[] | null): DisableScope | null {
  if (rules == null) {
    return {all: true, rules: new Set(), exceptions: new Set()};
  }
  if (rules.length === 0) {
    return null;
  }
  return {all: false, rules: new Set(rules), exceptions: new Set()};
}

export type IgnoreSpan = {startIndex: number, endIndex: number};

type IgnoreLineInfo = {
  lines: {text: string, start: number, end: number}[],
  /** True when a disable scope (not merely the marker itself) covers the line. */
  scopeDisabled: boolean[],
  /** Marker lines plus scope-disabled lines. */
  protectedLine: boolean[],
};

/**
 * @param {string} text - Note text
 * @param {string | null} ruleAlias - Rule being linted, or null for non-rule edits
 * @return {IgnoreLineInfo} Per-line disable and marker information
 */
function analyzeIgnoreLines(text: string, ruleAlias: string | null): IgnoreLineInfo {
  const regions = protectedRegions(text);
  const lines = splitLines(text);
  const scopes: DisableScope[] = [];
  const pending: {remaining: number, scope: DisableScope}[] = [];
  const scopeDisabled: boolean[] = new Array(lines.length).fill(false);
  const protectedLine: boolean[] = new Array(lines.length).fill(false);

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const activePending = pending.filter((entry) => entry.remaining > 0);
    const inProtected = rangesOverlap(line.start, line.end, regions);
    const marker = inProtected ? null : parseStandaloneMarker(line.text);

    const disabled = isRuleDisabled(scopes, ruleAlias) ||
      activePending.some((entry) => scopeDisables(entry.scope, ruleAlias));
    scopeDisabled[i] = disabled;
    protectedLine[i] = marker != null || disabled;

    for (const entry of pending) {
      entry.remaining--;
    }
    for (let p = pending.length - 1; p >= 0; p--) {
      if (pending[p].remaining <= 0) {
        pending.splice(p, 1);
      }
    }

    if (!marker) {
      continue;
    }

    if (marker.kind === 'disable') {
      const scope = makeScope(marker.rules);
      if (scope) {
        scopes.push(scope);
      }
      continue;
    }

    if (marker.kind === 'enable') {
      if (marker.rules == null) {
        scopes.pop();
      } else if (marker.rules.length > 0) {
        enableRules(scopes, marker.rules);
      }
      continue;
    }

    const scope = makeScope(marker.rules);
    if (!scope) {
      continue;
    }
    if (marker.kind === 'disable-next-line') {
      if (i + 1 < lines.length) {
        pending.push({remaining: 1, scope});
      }
      continue;
    }

    if (marker.lineCount != null && i + 1 < lines.length) {
      const available = lines.length - (i + 1);
      pending.push({remaining: Math.min(marker.lineCount, available), scope});
    }
  }

  return {lines, scopeDisabled, protectedLine};
}

/**
 * @param {{start: number, end: number}[]} lines - Line offsets
 * @param {boolean[]} flags - Whether each line is part of an ignore span
 * @return {IgnoreSpan[]} Coalesced spans, last in the file first
 */
function spansFromFlags(lines: {start: number, end: number}[], flags: boolean[]): IgnoreSpan[] {
  const spans: IgnoreSpan[] = [];
  let spanStart: number | null = null;
  let spanEnd = 0;
  for (let i = 0; i < lines.length; i++) {
    if (!flags[i]) {
      if (spanStart != null) {
        spans.push({startIndex: spanStart, endIndex: spanEnd});
        spanStart = null;
      }
      continue;
    }
    if (spanStart == null) {
      spanStart = lines[i].start;
    }
    spanEnd = lines[i].end;
  }
  if (spanStart != null) {
    spans.push({startIndex: spanStart, endIndex: spanEnd});
  }
  return spans.reverse();
}

/**
 * Spans that must not be modified for `ruleAlias`.
 * Pass null when the operation is not a named rule (custom regex): only
 * blanket disables and marker lines are protected.
 * Marker lines are always included.
 * @param {string} text - Note text
 * @param {string | null} ruleAlias - Rule being linted, or null for non-rule edits
 * @return {IgnoreSpan[]} Spans to leave unchanged, last in the file first
 */
export function getIgnoreSpansForRule(text: string, ruleAlias: string | null): IgnoreSpan[] {
  const info = analyzeIgnoreLines(text, ruleAlias);
  return spansFromFlags(info.lines, info.protectedLine);
}

/**
 * Spans where every rule is disabled (blanket scopes), including the marker
 * lines that bound those spans. Marker lines outside a blanket scope are omitted.
 * @param {string} text - Note text
 * @return {IgnoreSpan[]} Blanket ignore spans, last in the file first
 */
export function getBlanketIgnoreSpans(text: string): IgnoreSpan[] {
  const info = analyzeIgnoreLines(text, null);
  const spans = spansFromFlags(info.lines, info.protectedLine);
  return spans.filter((span) => {
    return info.lines.some((line, index) => {
      return info.scopeDisabled[index] && line.start >= span.startIndex && line.end <= span.endIndex;
    });
  });
}
