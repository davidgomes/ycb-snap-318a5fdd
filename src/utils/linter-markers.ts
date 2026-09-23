import type {Position} from 'unist';
import en from '../lang/locale/en';
import {getPositions, MDAstTypes} from './mdast';
import {yamlRegex} from './regex';

export type TextRange = {startIndex: number, endIndex: number};

type LinterMarker =
  | {type: 'disable', allRules: boolean, rules: string[]}
  | {type: 'enable', allRules: boolean, rules: string[]}
  | {type: 'disable-lines', allRules: boolean, rules: string[], numberOfLines: number}
  | {type: 'no-effect'};

type DisableScope = {
  allRules: boolean,
  // when allRules is true, this holds the rules that have been re-enabled within the scope
  rules: Set<string>,
};

type LineScopedDisable = DisableScope & {startLine: number, endLine: number};

const htmlMarkerLineRegex = /^[ \t]*<!--+(.*?)-{2,}>[ \t]*\r?$/;
const obsidianMarkerLineRegex = /^[ \t]*%%(.*?)%%[ \t]*\r?$/;
const markerDirectiveRegex = /^linter-(disable-next-n-lines|disable-next-line|disable|enable)(?![\w-])(.*)$/;
const numberOfLinesRegex = /^[ \t]*:[ \t]*(\S*)(.*)$/;
const markerCandidateRegex = /(?:<!--|%%)[ \t-]*linter-(?:disable|enable)/;

let knownRuleAliases: Set<string> = null;

function getKnownRuleAliases(): Set<string> {
  if (knownRuleAliases === null) {
    knownRuleAliases = new Set(Object.keys(en.rules).map((alias) => alias.toLowerCase()));
  }

  return knownRuleAliases;
}

/**
 * Parses a comma separated rule list.
 * @param {string} ruleListText The text following the marker directive
 * @return {string[] | null} null when no rule list was provided, otherwise the normalized list of known rule aliases
 */
function parseRuleList(ruleListText: string): string[] | null {
  const trimmedText = ruleListText.trim();
  if (trimmedText === '') {
    return null;
  }

  const knownAliases = getKnownRuleAliases();
  const rules = new Set<string>();
  for (const entry of trimmedText.split(',')) {
    const alias = entry.trim().toLowerCase();
    if (alias !== '' && knownAliases.has(alias)) {
      rules.add(alias);
    }
  }

  return [...rules];
}

/**
 * Parses a single line of text into a linter marker if the line is a standalone marker line.
 * @param {string} line The line to parse
 * @return {LinterMarker | null} The marker on the line or null when the line is not a marker line
 */
function parseMarkerLine(line: string): LinterMarker | null {
  let commentContent: string = null;
  const htmlMatch = line.match(htmlMarkerLineRegex);
  if (htmlMatch && !htmlMatch[1].includes('-->') && !htmlMatch[1].includes('<!--')) {
    commentContent = htmlMatch[1];
  } else {
    const obsidianMatch = line.match(obsidianMarkerLineRegex);
    if (obsidianMatch && !obsidianMatch[1].includes('%%')) {
      commentContent = obsidianMatch[1];
    }
  }

  if (commentContent === null) {
    return null;
  }

  const directiveMatch = commentContent.trim().match(markerDirectiveRegex);
  if (!directiveMatch) {
    return null;
  }

  const directive = directiveMatch[1];
  let rest = directiveMatch[2];
  let numberOfLines = 1;
  if (directive === 'disable-next-n-lines') {
    const numberOfLinesMatch = rest.match(numberOfLinesRegex);
    if (!numberOfLinesMatch || !/^\d+$/.test(numberOfLinesMatch[1])) {
      return {type: 'no-effect'};
    }

    numberOfLines = parseInt(numberOfLinesMatch[1], 10);
    rest = numberOfLinesMatch[2];
    if (numberOfLines <= 0) {
      return {type: 'no-effect'};
    }
  }

  if (rest !== '' && !/^[ \t]/.test(rest)) {
    return directive === 'disable-next-n-lines' ? {type: 'no-effect'} : null;
  }

  const rules = parseRuleList(rest);
  if (rules !== null && rules.length === 0) {
    return {type: 'no-effect'};
  }

  const allRules = rules === null;
  switch (directive) {
    case 'disable':
      return {type: 'disable', allRules, rules: rules ?? []};
    case 'enable':
      return {type: 'enable', allRules, rules: rules ?? []};
    default:
      return {type: 'disable-lines', allRules, rules: rules ?? [], numberOfLines};
  }
}

function getRegionsWhereMarkersAreIgnored(text: string): TextRange[] {
  const regions: TextRange[] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch) {
    regions.push({startIndex: 0, endIndex: yamlMatch[0].length});
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math, MDAstTypes.InlineMath]) {
    for (const position of getPositions(type, text) as Position[]) {
      regions.push({startIndex: position.start.offset, endIndex: position.end.offset});
    }
  }

  return regions;
}

function scopeDisablesRule(scope: DisableScope, ruleAlias: string | null): boolean {
  if (scope.allRules) {
    return ruleAlias === null || !scope.rules.has(ruleAlias);
  }

  return ruleAlias !== null && scope.rules.has(ruleAlias);
}

/**
 * Gets the ranges of text that the specified rule must leave untouched based on the linter markers in the text.
 * Marker lines themselves are always included in the returned ranges.
 * @param {string} text The text to get the ranges for
 * @param {string | null} ruleAlias The alias of the rule being run or null when not running a specific rule
 * in which case only markers that disable all rules apply
 * @return {TextRange[]} The ranges to ignore in document order
 */
export function getLinterMarkerIgnoreRanges(text: string, ruleAlias: string | null): TextRange[] {
  if (!markerCandidateRegex.test(text)) {
    return [];
  }

  ruleAlias = ruleAlias?.toLowerCase() ?? null;

  const lines = text.split('\n');
  const lastLineIndex = text.endsWith('\n') ? lines.length - 2 : lines.length - 1;
  const lineStartIndexes: number[] = new Array(lines.length);
  let offset = 0;
  for (let i = 0; i < lines.length; i++) {
    lineStartIndexes[i] = offset;
    offset += lines[i].length + 1;
  }

  let ignoredRegions: TextRange[] = null;
  const isInIgnoredRegion = (lineIndex: number): boolean => {
    if (ignoredRegions === null) {
      ignoredRegions = getRegionsWhereMarkersAreIgnored(text);
    }

    const start = lineStartIndexes[lineIndex];
    const end = start + lines[lineIndex].length;

    return ignoredRegions.some((region) => start < region.endIndex && end > region.startIndex);
  };

  const scopes: DisableScope[] = [];
  const lineScopedDisables: LineScopedDisable[] = [];
  const isProtected: boolean[] = new Array(lastLineIndex + 1).fill(false);

  for (let i = 0; i <= lastLineIndex; i++) {
    const marker = parseMarkerLine(lines[i]);
    if (marker === null || isInIgnoredRegion(i)) {
      if (scopes.some((scope) => scopeDisablesRule(scope, ruleAlias)) ||
          lineScopedDisables.some((disable) => disable.startLine <= i && i <= disable.endLine && scopeDisablesRule(disable, ruleAlias))) {
        isProtected[i] = true;
      }

      continue;
    }

    isProtected[i] = true;

    switch (marker.type) {
      case 'disable':
        scopes.push({allRules: marker.allRules, rules: new Set(marker.rules)});
        break;
      case 'enable':
        if (marker.allRules) {
          scopes.pop();
          break;
        }

        for (const rule of marker.rules) {
          for (let scopeIndex = scopes.length - 1; scopeIndex >= 0; scopeIndex--) {
            const scope = scopes[scopeIndex];
            if (!scopeDisablesRule(scope, rule)) {
              continue;
            }

            if (scope.allRules) {
              scope.rules.add(rule);
            } else {
              scope.rules.delete(rule);
              if (scope.rules.size === 0) {
                scopes.splice(scopeIndex, 1);
              }
            }

            break;
          }
        }
        break;
      case 'disable-lines':
        if (i < lastLineIndex) {
          lineScopedDisables.push({
            allRules: marker.allRules,
            rules: new Set(marker.rules),
            startLine: i + 1,
            endLine: Math.min(i + marker.numberOfLines, lastLineIndex),
          });
        }
        break;
    }
  }

  const ranges: TextRange[] = [];
  let rangeStartLine = -1;
  for (let i = 0; i <= lastLineIndex + 1; i++) {
    if (i <= lastLineIndex && isProtected[i]) {
      if (rangeStartLine === -1) {
        rangeStartLine = i;
      }
    } else if (rangeStartLine !== -1) {
      ranges.push({
        startIndex: lineStartIndexes[rangeStartLine],
        endIndex: lineStartIndexes[i - 1] + lines[i - 1].length,
      });
      rangeStartLine = -1;
    }
  }

  return ranges;
}
