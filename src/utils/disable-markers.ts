import {getPositions, MDAstTypes} from './mdast';
import {yamlRegex} from './regex';

type TextSection = {startIndex: number, endIndex: number};

type LineSpan = {startLine: number, endLine: number};

type Marker = {
  line: number,
  kind: 'disable' | 'enable' | 'disable-lines',
  // null when no rule list is present, meaning the marker applies to all rules
  rules: Set<string> | null,
  // only used by line-scoped disables where anything less than 1 is an invalid line count
  lineCount: number,
};

// when allRules is true, every rule except the ones in rules is disabled,
// otherwise only the rules in rules are disabled
type RuleSelection = {allRules: boolean, rules: Set<string>};

type DisabledLines = LineSpan & {selection: RuleSelection};

const markerLineRegex = /^[ \t]*(?:<!-{2,}(.*?)-{2,}>|%%(.*?)%%)[ \t]*$/;
const markerContentRegex = /^linter-(enable|disable-next-n-lines|disable-next-line|disable)(.*)$/;
const lineCountRegex = /^[ \t]*:[ \t]*(\d+)(?=[ \t]|$)(.*)$/;
const nodeTypesWithoutMarkers = [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math, MDAstTypes.InlineMath];

const knownRuleAliases = new Set<string>();

export function registerRuleAlias(alias: string): void {
  knownRuleAliases.add(alias);
}

/**
 * Gets the sections of the text that the specified rule must leave as is based on the linter disable and enable markers in the text.
 * A section is made up of consecutive whole lines which are either marker lines or lines that the markers disable the rule for.
 * @param {string} text - The text to get the disabled sections from.
 * @param {string | null} ruleAlias - The alias of the rule to get the disabled sections for or null to only include lines where all rules are disabled.
 * @return {{startIndex: number, endIndex: number}[]} The start and end indexes of each disabled section from the last to the earliest.
 */
export function getDisabledSectionsInText(text: string, ruleAlias: string | null): TextSection[] {
  const markerInfo = getMarkerInfo(text);
  if (!markerInfo) {
    return [];
  }

  const {lines, lineStarts, markers, codeAndMathSpans} = markerInfo;
  const isLineDisabled = new Array<boolean>(lines.length).fill(false);
  for (const marker of markers) {
    isLineDisabled[marker.line] = true;
  }

  for (const disabledLines of getDisabledLines(markers, lines.length, codeAndMathSpans)) {
    if (!selectionDisablesRule(disabledLines.selection, ruleAlias)) {
      continue;
    }

    isLineDisabled.fill(true, disabledLines.startLine, disabledLines.endLine + 1);
  }

  const disabledSections: TextSection[] = [];
  for (let line = 0; line < lines.length; line++) {
    if (!isLineDisabled[line]) {
      continue;
    }

    const startIndex = lineStarts[line];
    while (line + 1 < lines.length && isLineDisabled[line + 1]) {
      line++;
    }

    disabledSections.push({startIndex, endIndex: lineStarts[line] + lines[line].length});
  }

  return disabledSections.reverse();
}

/**
 * Moves blank lines that a rule added between a line-scoped disable marker and the line it disabled to before the marker,
 * so that the marker still disables the same line the next time the text is linted.
 * @param {string} originalText - The text before the rule was applied.
 * @param {string} newText - The text after the rule was applied.
 * @return {string} The new text with line-scoped disable markers directly before the lines they disable.
 */
export function keepLineScopedMarkersWithTheirLines(originalText: string, newText: string): string {
  if (originalText === newText) {
    return newText;
  }

  const original = getMarkerInfo(originalText);
  const isDirectlyBeforeContent = (lines: string[], line: number) => line + 1 < lines.length && lines[line + 1].trim() !== '';
  if (!original || !original.markers.some((marker) => marker.kind === 'disable-lines' && isDirectlyBeforeContent(original.lines, marker.line))) {
    return newText;
  }

  const updated = getMarkerInfo(newText);
  if (!updated || updated.markers.length !== original.markers.length) {
    return newText;
  }

  const lines = updated.lines;
  for (let i = original.markers.length - 1; i >= 0; i--) {
    const originalLine = original.markers[i].line;
    const line = updated.markers[i].line;
    if (original.markers[i].kind !== 'disable-lines' || original.lines[originalLine] !== lines[line] || !isDirectlyBeforeContent(original.lines, originalLine)) {
      continue;
    }

    let contentLine = line + 1;
    while (contentLine < lines.length && lines[contentLine].trim() === '') {
      contentLine++;
    }

    if (contentLine === line + 1 || contentLine === lines.length) {
      continue;
    }

    const addedBlankLines = lines.splice(line + 1, contentLine - line - 1);
    if (line > 0 && lines[line - 1].trim() !== '') {
      lines.splice(line, 0, ...addedBlankLines);
    }
  }

  return lines.join('\n') + (newText.endsWith('\n') ? '\n' : '');
}

function getMarkerInfo(text: string): {lines: string[], lineStarts: number[], markers: Marker[], codeAndMathSpans: LineSpan[]} | null {
  if (!text.includes('linter-')) {
    return null;
  }

  const lines = text.split('\n');
  // a trailing line break ends the last line rather than starting a new one
  if (text.endsWith('\n')) {
    lines.pop();
  }

  const lineStarts: number[] = [];
  let lineStart = 0;
  const possibleMarkers: Marker[] = [];
  for (let i = 0; i < lines.length; i++) {
    lineStarts.push(lineStart);
    lineStart += lines[i].length + 1;

    const marker = parseMarker(lines[i], i);
    if (marker) {
      possibleMarkers.push(marker);
    }
  }

  if (possibleMarkers.length === 0) {
    return null;
  }

  const [sectionsWithoutMarkers, codeAndMathSpans] = getSectionsWithoutMarkers(text);
  const markers = possibleMarkers.filter((marker) => {
    const markerStart = lineStarts[marker.line] + lines[marker.line].length - lines[marker.line].trimStart().length;

    return !sectionsWithoutMarkers.some((section) => section.startIndex <= markerStart && markerStart < section.endIndex);
  });

  if (markers.length === 0) {
    return null;
  }

  return {lines, lineStarts, markers, codeAndMathSpans};
}

function parseMarker(lineText: string, line: number): Marker | null {
  const lineMatch = lineText.match(markerLineRegex);
  if (!lineMatch) {
    return null;
  }

  const isHtmlComment = lineMatch[1] !== undefined;
  const commentContent = isHtmlComment ? lineMatch[1] : lineMatch[2];
  // make sure there is only a single comment on the line
  if (commentContent.includes(isHtmlComment ? '-->' : '%%')) {
    return null;
  }

  const contentMatch = commentContent.trim().match(markerContentRegex);
  if (!contentMatch) {
    return null;
  }

  const directive = contentMatch[1];
  const ruleList = contentMatch[2];
  if (directive === 'disable-next-n-lines') {
    if (ruleList !== '' && !/^[ \t:]/.test(ruleList)) {
      return null;
    }

    const lineCountMatch = ruleList.match(lineCountRegex);
    if (!lineCountMatch) {
      return {line, kind: 'disable-lines', rules: null, lineCount: 0};
    }

    return {line, kind: 'disable-lines', rules: parseRuleList(lineCountMatch[2]), lineCount: parseInt(lineCountMatch[1], 10)};
  }

  if (ruleList !== '' && !/^[ \t]/.test(ruleList)) {
    return null;
  }

  switch (directive) {
    case 'disable-next-line':
      return {line, kind: 'disable-lines', rules: parseRuleList(ruleList), lineCount: 1};
    case 'disable':
      return {line, kind: 'disable', rules: parseRuleList(ruleList), lineCount: 0};
    default:
      return {line, kind: 'enable', rules: parseRuleList(ruleList), lineCount: 0};
  }
}

function parseRuleList(ruleList: string): Set<string> | null {
  if (ruleList.trim() === '') {
    return null;
  }

  const rules = new Set<string>();
  for (const listedRule of ruleList.split(',')) {
    const alias = listedRule.trim().toLowerCase();
    if (knownRuleAliases.has(alias)) {
      rules.add(alias);
    }
  }

  return rules;
}

/**
 * Gets the sections of the text in which markers are not recognized which are YAML frontmatter, code, and math.
 * @param {string} text - The text to get the sections from.
 * @return {[TextSection[], LineSpan[]]} The sections along with the lines spanned by the code and math in the text sorted by starting line.
 */
function getSectionsWithoutMarkers(text: string): [TextSection[], LineSpan[]] {
  const sections: TextSection[] = [];
  const codeAndMathSpans: LineSpan[] = [];

  const yaml = text.match(yamlRegex);
  if (yaml) {
    sections.push({startIndex: yaml.index, endIndex: yaml.index + yaml[0].length});
  }

  for (const nodeType of nodeTypesWithoutMarkers) {
    for (const position of getPositions(nodeType, text)) {
      sections.push({startIndex: position.start.offset, endIndex: position.end.offset});
      codeAndMathSpans.push({startLine: position.start.line - 1, endLine: position.end.line - 1});
    }
  }

  codeAndMathSpans.sort((a, b) => a.startLine - b.startLine);

  return [sections, codeAndMathSpans];
}

function getDisabledLines(markers: Marker[], lineCount: number, codeAndMathSpans: LineSpan[]): DisabledLines[] {
  const lastLine = lineCount - 1;
  const disabledLines: DisabledLines[] = [];
  const openScopes: {startLine: number, selection: RuleSelection}[] = [];
  const addDisabledLinesForScope = (scope: {startLine: number, selection: RuleSelection}, endLine: number) => {
    if (scope.startLine <= endLine) {
      disabledLines.push({startLine: scope.startLine, endLine, selection: {allRules: scope.selection.allRules, rules: new Set(scope.selection.rules)}});
    }
  };

  for (const marker of markers) {
    // a rule list that was provided, but does not contain any known rules
    if (marker.rules && marker.rules.size === 0) {
      continue;
    }

    if (marker.kind === 'disable') {
      openScopes.push({startLine: marker.line + 1, selection: getRuleSelection(marker.rules)});
    } else if (marker.kind === 'disable-lines') {
      if (marker.lineCount < 1 || marker.line >= lastLine) {
        continue;
      }

      const startLine = marker.line + 1;
      let endLine = Math.min(marker.line + marker.lineCount, lastLine);
      // disabling only part of code or math would change how the rest of the text is parsed
      for (const span of codeAndMathSpans) {
        if (span.startLine <= endLine && span.endLine > endLine && span.endLine >= startLine) {
          endLine = span.endLine;
        }
      }

      disabledLines.push({startLine, endLine, selection: getRuleSelection(marker.rules)});
    } else if (!marker.rules) {
      const scope = openScopes.pop();
      if (scope) {
        addDisabledLinesForScope(scope, marker.line - 1);
      }
    } else {
      for (const alias of marker.rules) {
        for (let i = openScopes.length - 1; i >= 0; i--) {
          const scope = openScopes[i];
          if (!selectionDisablesRule(scope.selection, alias)) {
            continue;
          }

          addDisabledLinesForScope(scope, marker.line - 1);
          scope.startLine = marker.line + 1;
          if (scope.selection.allRules) {
            scope.selection.rules.add(alias);
          } else {
            scope.selection.rules.delete(alias);
            if (scope.selection.rules.size === 0) {
              openScopes.splice(i, 1);
            }
          }

          break;
        }
      }
    }
  }

  for (const scope of openScopes) {
    addDisabledLinesForScope(scope, lastLine);
  }

  return disabledLines;
}

function getRuleSelection(rules: Set<string> | null): RuleSelection {
  return rules ? {allRules: false, rules: new Set(rules)} : {allRules: true, rules: new Set()};
}

function selectionDisablesRule(selection: RuleSelection, ruleAlias: string | null): boolean {
  if (selection.allRules) {
    return ruleAlias === null || !selection.rules.has(ruleAlias);
  }

  return ruleAlias !== null && selection.rules.has(ruleAlias);
}
