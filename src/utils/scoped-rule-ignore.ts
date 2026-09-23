import {getPositions, MDAstTypes} from './mdast';
import {rules} from '../rules';

/**
 * Character range in the original text. `end` is exclusive.
 */
export type IgnoreRange = {
  start: number,
  end: number,
};

type LineSpan = {
  text: string,
  start: number,
  /**
   * Exclusive end of this line's contents.
   * Points at the line's `\n` when there is one (so a preceding `\r` is included), or at `text.length` for the last line.
   */
  lineEnd: number,
};

type MarkerKind = 'disable' | 'enable' | 'disable-next-line' | 'disable-next-n-lines';

type ParsedMarker = {
  lineIndex: number,
  kind: MarkerKind,
  /** True when the marker was written without a rule list, which means every rule. */
  omittedRuleList: boolean,
  rules: Set<string>,
  lineCount: number | null,
  effective: boolean,
};

/**
 * Open disable scope.
 * For `disablesAll`, `rules` is the set of aliases re-enabled inside that scope.
 * Otherwise `rules` is the set of aliases this scope disables.
 */
type DisableScope = {
  disablesAll: boolean,
  rules: Set<string>,
};

type LineOverlay = {
  all: boolean,
  rules: Set<string>,
};

const MARKER_LINE_REGEX = /^[ \t]*(<!--+|%%)[ \t]*(linter-disable-next-n-lines|linter-disable-next-line|linter-disable|linter-enable)(?=[ \t]|:|--+>|%%)([\s\S]*?)(--+>|%%)[ \t]*$/;

/**
 * Ranges that must not be modified for `ruleAlias`.
 * A null alias protects marker lines and regions where every rule is disabled (custom replacements).
 * Ranges are in document order and `end` is exclusive.
 * @param {string} text The note text.
 * @param {string | null} ruleAlias The rule alias being applied, or null when the caller is not a single rule.
 * @return {IgnoreRange[]} Protected ranges in document order.
 */
export function getScopedIgnoreRanges(text: string, ruleAlias: string | null): IgnoreRange[] {
  const lines = getLineSpans(text);
  if (lines.length === 0) {
    return [];
  }

  const knownAliases = getKnownRuleAliases();
  const excluded = getExcludedRanges(text);
  const markers = parseMarkers(lines, excluded, knownAliases);
  const protectedLines = computeProtectedLines(lines.length, markers, ruleAlias);
  return mergeProtectedLines(lines, protectedLines);
}

/**
 * Same ranges as {@link getScopedIgnoreRanges}, last range first, so callers can replace without shifting indexes.
 * @param {string} text The note text.
 * @param {string | null} ruleAlias The rule alias being applied, or null when the caller is not a single rule.
 * @return {{startIndex: number, endIndex: number}[]} Protected ranges from the end of the note toward the start.
 */
export function getAllCustomIgnoreSectionsInText(text: string, ruleAlias: string | null = null): {startIndex: number, endIndex: number}[] {
  return getScopedIgnoreRanges(text, ruleAlias).reverse().map((range) => {
    return {startIndex: range.start, endIndex: range.end};
  });
}

function getKnownRuleAliases(): Set<string> {
  const known = new Set<string>();
  if (!rules) {
    return known;
  }

  for (const rule of rules) {
    if (rule?.alias) {
      known.add(rule.alias.toLowerCase());
    }
  }

  return known;
}

function getLineSpans(text: string): LineSpan[] {
  const lines: LineSpan[] = [];
  let start = 0;
  for (let index = 0; index < text.length; index++) {
    if (text[index] !== '\n') {
      continue;
    }

    let contentEnd = index;
    if (contentEnd > start && text[contentEnd - 1] === '\r') {
      contentEnd--;
    }

    lines.push({
      text: text.slice(start, contentEnd),
      start: start,
      lineEnd: index,
    });
    start = index + 1;
  }

  if (start < text.length) {
    let contentEnd = text.length;
    if (text[contentEnd - 1] === '\r') {
      contentEnd--;
    }

    lines.push({
      text: text.slice(start, contentEnd),
      start: start,
      lineEnd: text.length,
    });
  }

  return lines;
}

function getExcludedRanges(text: string): IgnoreRange[] {
  const ranges: IgnoreRange[] = [];
  const frontmatter = getYamlFrontmatterRange(text);
  if (frontmatter) {
    ranges.push(frontmatter);
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      const start = position?.start?.offset;
      const end = position?.end?.offset;
      if (start == null || end == null || end <= start) {
        continue;
      }

      ranges.push({start: start, end: end});
    }
  }

  return ranges;
}

function getYamlFrontmatterRange(text: string): IgnoreRange | null {
  const firstBreak = text.indexOf('\n');
  const firstLineEnd = firstBreak === -1 ? text.length : firstBreak;
  let firstContentEnd = firstLineEnd;
  if (firstContentEnd > 0 && text[firstContentEnd - 1] === '\r') {
    firstContentEnd--;
  }

  if (!/^---[ \t]*$/.test(text.slice(0, firstContentEnd)) || firstBreak === -1) {
    return null;
  }

  let index = firstBreak + 1;
  while (index < text.length) {
    const newline = text.indexOf('\n', index);
    const lineEnd = newline === -1 ? text.length : newline;
    let contentEnd = lineEnd;
    if (contentEnd > index && text[contentEnd - 1] === '\r') {
      contentEnd--;
    }

    if (/^---[ \t]*$/.test(text.slice(index, contentEnd))) {
      return {start: 0, end: contentEnd};
    }

    if (newline === -1) {
      break;
    }

    index = newline + 1;
  }

  return null;
}

function rangeOverlaps(start: number, end: number, ranges: IgnoreRange[]): boolean {
  for (const range of ranges) {
    if (start < range.end && range.start < end) {
      return true;
    }
  }

  return false;
}

function parseMarkers(lines: LineSpan[], excluded: IgnoreRange[], knownAliases: Set<string>): ParsedMarker[] {
  const markers: ParsedMarker[] = [];
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
    const line = lines[lineIndex];
    if (line.start === line.lineEnd) {
      continue;
    }

    if (rangeOverlaps(line.start, line.lineEnd, excluded)) {
      continue;
    }

    const parsed = parseMarkerLine(line.text, knownAliases);
    if (!parsed) {
      continue;
    }

    markers.push({
      lineIndex: lineIndex,
      kind: parsed.kind,
      omittedRuleList: parsed.omittedRuleList,
      rules: parsed.rules,
      lineCount: parsed.lineCount,
      effective: parsed.effective,
    });
  }

  return markers;
}

function parseMarkerLine(line: string, knownAliases: Set<string>): Omit<ParsedMarker, 'lineIndex'> | null {
  const match = MARKER_LINE_REGEX.exec(line);
  if (!match) {
    return null;
  }

  const open = match[1];
  const keyword = match[2];
  const payload = match[3] ?? '';
  const close = match[4];
  const openIsHtml = open.startsWith('<!');
  const closeIsHtml = close.endsWith('>');
  if (openIsHtml !== closeIsHtml) {
    return null;
  }

  let kind: MarkerKind;
  if (keyword === 'linter-disable') {
    kind = 'disable';
  } else if (keyword === 'linter-enable') {
    kind = 'enable';
  } else if (keyword === 'linter-disable-next-line') {
    kind = 'disable-next-line';
  } else {
    kind = 'disable-next-n-lines';
  }

  let rulesSource = payload;
  let lineCount: number | null = null;
  let countIsValid = true;
  if (kind === 'disable-next-n-lines') {
    const parsedCount = splitNextNPayload(payload);
    lineCount = parsedCount.count;
    countIsValid = parsedCount.valid;
    rulesSource = parsedCount.rulesSource;
  }

  const normalized = normalizeRuleList(rulesSource, knownAliases);
  let effective = countIsValid;
  if (!normalized.omitted && normalized.rules.size === 0) {
    effective = false;
  }

  return {
    kind: kind,
    omittedRuleList: normalized.omitted,
    rules: normalized.rules,
    lineCount: lineCount,
    effective: effective,
  };
}

function splitNextNPayload(payload: string): {valid: boolean, count: number | null, rulesSource: string} {
  const match = /^[ \t]*:[ \t]*([^\s,]+)?([\s\S]*)$/.exec(payload);
  if (!match || match[1] == null) {
    return {valid: false, count: null, rulesSource: ''};
  }

  const count = parsePositiveBase10Integer(match[1]);
  let rulesSource = match[2] ?? '';
  rulesSource = rulesSource.replace(/^[ \t]*,?[ \t]*/, '');
  if (count == null) {
    return {valid: false, count: null, rulesSource: rulesSource};
  }

  return {valid: true, count: count, rulesSource: rulesSource};
}

function parsePositiveBase10Integer(token: string): number | null {
  if (!/^[0-9]+$/.test(token) || /^0+$/.test(token)) {
    return null;
  }

  const value = Number(token);
  if (!Number.isSafeInteger(value)) {
    return Number.MAX_SAFE_INTEGER;
  }

  return value;
}

function normalizeRuleList(raw: string, knownAliases: Set<string>): {omitted: boolean, rules: Set<string>} {
  if (raw.trim() === '') {
    return {omitted: true, rules: new Set()};
  }

  const normalized = new Set<string>();
  for (const part of raw.split(',')) {
    const alias = part.trim().toLowerCase();
    if (alias === '' || !knownAliases.has(alias)) {
      continue;
    }

    normalized.add(alias);
  }

  return {omitted: false, rules: normalized};
}

function computeProtectedLines(lineCount: number, markers: ParsedMarker[], ruleAlias: string | null): Set<number> {
  const protectedLines = new Set<number>();
  const scopes: DisableScope[] = [];
  const lineOverlay = new Map<number, LineOverlay>();
  const markersByLine = new Map<number, ParsedMarker>();
  for (const marker of markers) {
    markersByLine.set(marker.lineIndex, marker);
  }

  const alias = ruleAlias == null ? null : ruleAlias.toLowerCase();
  for (let lineIndex = 0; lineIndex < lineCount; lineIndex++) {
    const marker = markersByLine.get(lineIndex);
    if (marker) {
      protectedLines.add(lineIndex);
      if (marker.effective) {
        applyMarker(marker, scopes, lineOverlay, lineCount);
      }
      continue;
    }

    if (isContentLineDisabled(scopes, lineOverlay.get(lineIndex), alias)) {
      protectedLines.add(lineIndex);
    }
  }

  return protectedLines;
}

function applyMarker(marker: ParsedMarker, scopes: DisableScope[], lineOverlay: Map<number, LineOverlay>, lineCount: number): void {
  if (marker.kind === 'disable') {
    scopes.push(marker.omittedRuleList ? {disablesAll: true, rules: new Set()} : {disablesAll: false, rules: new Set(marker.rules)});
    return;
  }

  if (marker.kind === 'enable') {
    if (marker.omittedRuleList) {
      scopes.pop();
      return;
    }

    for (const rule of marker.rules) {
      removeRuleFromNearestScope(scopes, rule);
    }
    return;
  }

  const count = marker.kind === 'disable-next-line' ? 1 : marker.lineCount;
  if (count == null || count <= 0) {
    return;
  }

  const first = marker.lineIndex + 1;
  if (first >= lineCount) {
    return;
  }

  const last = Math.min(lineCount - 1, marker.lineIndex + count);
  for (let index = first; index <= last; index++) {
    const overlay = overlayFor(lineOverlay, index);
    if (marker.omittedRuleList) {
      overlay.all = true;
      continue;
    }

    for (const rule of marker.rules) {
      overlay.rules.add(rule);
    }
  }
}

function overlayFor(lineOverlay: Map<number, LineOverlay>, lineIndex: number): LineOverlay {
  let overlay = lineOverlay.get(lineIndex);
  if (!overlay) {
    overlay = {all: false, rules: new Set()};
    lineOverlay.set(lineIndex, overlay);
  }

  return overlay;
}

function removeRuleFromNearestScope(scopes: DisableScope[], rule: string): void {
  for (let index = scopes.length - 1; index >= 0; index--) {
    const scope = scopes[index];
    if (scope.disablesAll) {
      if (scope.rules.has(rule)) {
        continue;
      }

      scope.rules.add(rule);
      return;
    }

    if (!scope.rules.has(rule)) {
      continue;
    }

    scope.rules.delete(rule);
    if (scope.rules.size === 0) {
      scopes.splice(index, 1);
    }
    return;
  }
}

function isContentLineDisabled(scopes: DisableScope[], overlay: LineOverlay | undefined, alias: string | null): boolean {
  if (alias == null) {
    if (overlay?.all) {
      return true;
    }

    return scopes.some((scope) => scope.disablesAll);
  }

  if (overlay?.all || overlay?.rules.has(alias)) {
    return true;
  }

  for (const scope of scopes) {
    if (scope.disablesAll) {
      if (!scope.rules.has(alias)) {
        return true;
      }
      continue;
    }

    if (scope.rules.has(alias)) {
      return true;
    }
  }

  return false;
}

function mergeProtectedLines(lines: LineSpan[], protectedLines: Set<number>): IgnoreRange[] {
  const indexes = Array.from(protectedLines).sort((left, right) => left - right);
  const ranges: IgnoreRange[] = [];
  let rangeStart = -1;
  let rangeEnd = -1;
  let previousIndex = -1;

  for (const index of indexes) {
    const line = lines[index];
    if (rangeStart === -1) {
      rangeStart = line.start;
      rangeEnd = line.lineEnd;
      previousIndex = index;
      continue;
    }

    if (index === previousIndex + 1) {
      rangeEnd = line.lineEnd;
      previousIndex = index;
      continue;
    }

    if (rangeStart !== rangeEnd) {
      ranges.push({start: rangeStart, end: rangeEnd});
    }
    rangeStart = line.start;
    rangeEnd = line.lineEnd;
    previousIndex = index;
  }

  if (rangeStart !== -1 && rangeStart !== rangeEnd) {
    ranges.push({start: rangeStart, end: rangeEnd});
  }

  return ranges;
}
