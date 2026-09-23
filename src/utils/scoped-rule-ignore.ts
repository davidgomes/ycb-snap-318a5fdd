import en from '../lang/locale/en';
import {getLinterMarkerIgnoredRanges} from './mdast';
import {escapeRegExp} from './regex';

/**
 * Placeholder swapped in for lines a rule must not modify.
 * Restored after the rule runs.
 */
export const SCOPED_RULE_IGNORE_PLACEHOLDER = '{SCOPED_RULE_IGNORE_PLACEHOLDER}';

const MARKER_LINE_REGEX = /^[ \t]*(<!-{2,}|%%)[ \t]*linter-(disable-next-n-lines|disable-next-line|disable|enable)(.*?)(-{2,}>|%%)[ \t]*$/;

type MarkerKind = 'disable' | 'enable' | 'disable-next-line' | 'disable-next-n-lines';

type ParsedMarker = {
  kind: MarkerKind,
  /** `null` when the marker omits a rule list (disable-all or pop-scope). */
  ruleList: string[] | null,
  /** Set for `disable-next-n-lines`. `null` when `N` is not a positive integer. */
  lineCount: number | null,
};

type DisableScope = {
  all: boolean,
  rules: Set<string>,
  exceptions: Set<string>,
};

type TextLine = {
  text: string,
  start: number,
  end: number,
};

type CharacterRange = {
  start: number,
  end: number,
};

let cachedKnownRuleAliases: Set<string> | null = null;

/**
 * Rule aliases the linter knows about, compared case-insensitively.
 * @return {Set<string>} Lowercase rule aliases
 */
export function getKnownRuleAliases(): Set<string> {
  if (!cachedKnownRuleAliases) {
    cachedKnownRuleAliases = new Set(Object.keys(en.rules).map((alias) => alias.toLowerCase()));
  }

  return cachedKnownRuleAliases;
}

/**
 * Runs `func` with lines disabled for `ruleAlias`, and with every recognized marker line preserved.
 * @param {string} text The file text
 * @param {string} ruleAlias The alias of the rule about to run
 * @param {ReadonlySet<string>} knownRuleAliases Lowercase aliases that may appear in marker rule lists
 * @param {function(string): string} func The rule body
 * @return {string} The linted text with ignored regions restored
 */
export function applyScopedRuleIgnores(text: string, ruleAlias: string, knownRuleAliases: ReadonlySet<string>, func: (text: string) => string): string {
  if (!text.includes('linter-disable') && !text.includes('linter-enable')) {
    return func(text);
  }

  const lines = splitLines(text);
  const protectedLines = getProtectedLineIndexes(text, lines, ruleAlias.toLowerCase(), knownRuleAliases);
  const ranges = mergeProtectedLines(lines, protectedLines);
  if (ranges.length === 0) {
    return func(text);
  }

  const replacedValues = ranges.map((range) => text.substring(range.start, range.end));
  let maskedText = text;
  for (let index = ranges.length - 1; index >= 0; index--) {
    const range = ranges[index];
    maskedText = maskedText.substring(0, range.start) + SCOPED_RULE_IGNORE_PLACEHOLDER + maskedText.substring(range.end);
  }

  let restored = func(maskedText);
  const placeholderRegex = new RegExp(escapeRegExp(SCOPED_RULE_IGNORE_PLACEHOLDER), 'i');
  for (const value of replacedValues) {
    restored = restored.replace(placeholderRegex, () => value);
  }

  return restored;
}

function splitLines(text: string): TextLine[] {
  const lines: TextLine[] = [];
  let start = 0;
  for (let index = 0; index < text.length; index++) {
    if (text[index] !== '\n') {
      continue;
    }

    let end = index;
    if (end > start && text[end - 1] === '\r') {
      end--;
    }
    lines.push({text: text.slice(start, end), start, end});
    start = index + 1;
  }

  if (start < text.length || lines.length === 0) {
    let end = text.length;
    if (end > start && text[end - 1] === '\r') {
      end--;
    }
    lines.push({text: text.slice(start, end), start, end});
  }

  return lines;
}

function getProtectedLineIndexes(text: string, lines: TextLine[], ruleAlias: string, knownRuleAliases: ReadonlySet<string>): Set<number> {
  const maskRanges = getMaskRanges(text);
  const stack: DisableScope[] = [];
  const lineScopedDisabled = new Set<number>();
  const protectedLines = new Set<number>();

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (!overlapsRange(line.start, line.end, maskRanges)) {
      const marker = parseMarker(line.text);
      if (marker) {
        protectedLines.add(index);
        applyMarker(marker, index, lines.length, stack, lineScopedDisabled, ruleAlias, knownRuleAliases);
        continue;
      }
    }

    if (lineScopedDisabled.has(index) || stackDisablesAlias(stack, ruleAlias)) {
      protectedLines.add(index);
    }
  }

  return protectedLines;
}

function applyMarker(marker: ParsedMarker, lineIndex: number, lineCount: number, stack: DisableScope[], lineScopedDisabled: Set<number>, ruleAlias: string, knownRuleAliases: ReadonlySet<string>): void {
  if (marker.kind === 'disable') {
    pushDisableScope(stack, marker.ruleList, knownRuleAliases);
    return;
  }

  if (marker.kind === 'enable') {
    if (marker.ruleList === null) {
      stack.pop();
      return;
    }

    const rules = normalizeRuleList(marker.ruleList, knownRuleAliases);
    if (rules.size === 0) {
      return;
    }
    enableRules(stack, rules);
    return;
  }

  const count = marker.kind === 'disable-next-line' ? 1 : marker.lineCount;
  if (count === null || count <= 0 || lineIndex + 1 >= lineCount) {
    return;
  }

  let appliesToRule = false;
  if (marker.ruleList === null) {
    appliesToRule = true;
  } else {
    const rules = normalizeRuleList(marker.ruleList, knownRuleAliases);
    appliesToRule = rules.has(ruleAlias);
  }
  if (!appliesToRule) {
    return;
  }

  const lastLine = Math.min(lineCount - 1, lineIndex + count);
  for (let index = lineIndex + 1; index <= lastLine; index++) {
    lineScopedDisabled.add(index);
  }
}

function pushDisableScope(stack: DisableScope[], ruleList: string[] | null, knownRuleAliases: ReadonlySet<string>): void {
  if (ruleList === null) {
    stack.push({all: true, rules: new Set(), exceptions: new Set()});
    return;
  }

  const rules = normalizeRuleList(ruleList, knownRuleAliases);
  if (rules.size === 0) {
    return;
  }
  stack.push({all: false, rules, exceptions: new Set()});
}

function enableRules(stack: DisableScope[], rules: Set<string>): void {
  for (const rule of rules) {
    for (let index = stack.length - 1; index >= 0; index--) {
      const scope = stack[index];
      if (scope.all) {
        if (!scope.exceptions.has(rule)) {
          scope.exceptions.add(rule);
          break;
        }
        continue;
      }

      if (scope.rules.has(rule)) {
        scope.rules.delete(rule);
        if (scope.rules.size === 0) {
          stack.splice(index, 1);
        }
        break;
      }
    }
  }
}

function stackDisablesAlias(stack: DisableScope[], ruleAlias: string): boolean {
  for (const scope of stack) {
    if (scope.all) {
      if (!scope.exceptions.has(ruleAlias)) {
        return true;
      }
    } else if (scope.rules.has(ruleAlias)) {
      return true;
    }
  }

  return false;
}

function normalizeRuleList(ruleList: string[], knownRuleAliases: ReadonlySet<string>): Set<string> {
  const normalized = new Set<string>();
  for (const entry of ruleList) {
    const alias = entry.trim().toLowerCase();
    if (alias === '' || !knownRuleAliases.has(alias)) {
      continue;
    }
    normalized.add(alias);
  }

  return normalized;
}

function parseMarker(line: string): ParsedMarker | null {
  const match = line.match(MARKER_LINE_REGEX);
  if (!match) {
    return null;
  }

  const opener = match[1];
  const kind = match[2] as MarkerKind;
  const rest = match[3];
  const closer = match[4];
  if (opener.startsWith('<!') !== closer.endsWith('>')) {
    return null;
  }

  if (kind === 'disable-next-n-lines') {
    const countMatch = rest.match(/^[ \t]*:[ \t]*([^\s,]*)(.*)$/);
    if (!countMatch) {
      return null;
    }

    return {
      kind,
      ruleList: parseRuleList(countMatch[2] ?? ''),
      lineCount: parsePositiveInteger(countMatch[1]),
    };
  }

  return {
    kind,
    ruleList: parseRuleList(rest),
    lineCount: null,
  };
}

function parseRuleList(rest: string): string[] | null {
  if (rest.trim() === '') {
    return null;
  }

  return rest.split(',');
}

function parsePositiveInteger(token: string): number | null {
  // Reject leading zeros so the token is a canonical base-10 integer (`08` is not `8`).
  if (!/^[1-9][0-9]*$/.test(token)) {
    return null;
  }

  const value = Number(token);
  if (!Number.isFinite(value) || value <= 0) {
    return null;
  }
  if (!Number.isSafeInteger(value)) {
    return Number.MAX_SAFE_INTEGER;
  }

  return value;
}

function getMaskRanges(text: string): CharacterRange[] {
  return getLinterMarkerIgnoredRanges(text);
}

function overlapsRange(start: number, end: number, ranges: CharacterRange[]): boolean {
  if (start === end) {
    return false;
  }

  for (const range of ranges) {
    if (start < range.end && range.start < end) {
      return true;
    }
  }

  return false;
}

function mergeProtectedLines(lines: TextLine[], protectedLines: Set<number>): CharacterRange[] {
  const indexes = [...protectedLines].sort((left, right) => left - right);
  const ranges: CharacterRange[] = [];
  let groupStart: number | null = null;
  let previous: number | null = null;

  const pushGroup = () => {
    if (groupStart === null || previous === null) {
      return;
    }
    ranges.push({start: lines[groupStart].start, end: lines[previous].end});
  };

  for (const index of indexes) {
    if (previous === null || index !== previous + 1) {
      pushGroup();
      groupStart = index;
    }
    previous = index;
  }
  pushGroup();

  return ranges;
}
