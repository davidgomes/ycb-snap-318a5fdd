import {getPositions, MDAstTypes} from './mdast';
import {yamlRegex} from './regex';

const markerRegex = /^[ \t]*(?:<!--[ \t]*(linter-[^\n]*?)[ \t]*-->|%%[ \t]*(linter-[^\n]*?)[ \t]*%%)[ \t]*$/;
const markerBodyRegex = /^linter-(disable-next-n-lines|disable-next-line|disable|enable)(?::|(?=[ \t]|$))([^\n]*)$/;

type Scope = {all: boolean, rules: Set<string>, exceptions: Set<string>};
type Marker = {command: string, rules: string[] | null, count?: number};

function parseRuleList(list: string, knownAliases: Set<string>): string[] | null {
  const trimmed = list.trim();
  if (trimmed === '') {
    return null;
  }

  const result = new Set<string>();
  for (const entry of trimmed.split(',')) {
    const alias = entry.trim().toLowerCase();
    if (alias !== '' && knownAliases.has(alias)) {
      result.add(alias);
    }
  }

  return [...result];
}

function parseMarker(line: string, knownAliases: Set<string>): Marker | null {
  const lineMatch = line.match(markerRegex);
  if (!lineMatch) {
    return null;
  }

  const bodyMatch = (lineMatch[1] ?? lineMatch[2]).match(markerBodyRegex);
  if (!bodyMatch) {
    return null;
  }

  const command = bodyMatch[1];
  let rest = bodyMatch[2];
  const hasColon = (lineMatch[1] ?? lineMatch[2]).charAt(('linter-' + command).length) === ':';
  if (command === 'disable-next-n-lines') {
    if (!hasColon) {
      return {command: 'invalid', rules: null};
    }

    const countMatch = rest.match(/^[ \t]*(\d+)(?=[ \t]|$)/);
    const count = countMatch ? parseInt(countMatch[1], 10) : 0;
    if (!countMatch || count < 1) {
      return {command: 'invalid', rules: null};
    }

    rest = rest.substring(countMatch[0].length);
    return {command, rules: parseRuleList(rest, knownAliases), count};
  } else if (hasColon) {
    return null;
  }

  return {command, rules: parseRuleList(rest, knownAliases)};
}

function getExcludedRanges(text: string): [number, number][] {
  const ranges: [number, number][] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch) {
    ranges.push([yamlMatch.index, yamlMatch.index + yamlMatch[0].length]);
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.InlineCode, MDAstTypes.Math, MDAstTypes.InlineMath]) {
    for (const position of getPositions(type, text)) {
      ranges.push([position.start.offset, position.end.offset]);
    }
  }

  return ranges;
}

/**
 * Determines which lines should be left untouched by the rule with the given alias
 * based on linter-disable/linter-enable comment markers. Marker lines are always protected.
 * @param {string} text The text to check for markers
 * @param {string} alias The alias of the rule being applied
 * @param {Set<string>} knownAliases All valid rule aliases
 * @return {boolean[]} Whether each line of the text is protected from the rule
 */
export function getProtectedLines(text: string, alias: string, knownAliases: Set<string>): boolean[] {
  const lines = text.split('\n');
  const result: boolean[] = new Array(lines.length).fill(false);
  if (!text.includes('linter-')) {
    return result;
  }

  const excludedRanges = getExcludedRanges(text);
  const scopes: Scope[] = [];
  let lineScopedUntil = -1;

  const scopeDisables = (scope: Scope, rule: string) => scope.all ? !scope.exceptions.has(rule) : scope.rules.has(rule);

  let offset = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineStart = offset;
    const lineEnd = offset + line.length;
    offset = lineEnd + 1;

    const inExcluded = excludedRanges.some(([start, end]) => start < lineEnd && end > lineStart);
    const marker = inExcluded ? null : parseMarker(line, knownAliases);
    if (marker) {
      result[i] = true;
      const affectsRule = marker.rules === null || marker.rules.includes(alias);
      const hasEffect = marker.rules === null || marker.rules.length > 0;
      switch (marker.command) {
        case 'disable':
          if (hasEffect) {
            scopes.push({all: marker.rules === null, rules: new Set(marker.rules ?? []), exceptions: new Set()});
          }
          break;
        case 'enable':
          if (marker.rules === null) {
            scopes.pop();
          } else {
            for (const rule of marker.rules) {
              for (let s = scopes.length - 1; s >= 0; s--) {
                const scope = scopes[s];
                if (!scopeDisables(scope, rule)) {
                  continue;
                }

                if (scope.all) {
                  scope.exceptions.add(rule);
                } else {
                  scope.rules.delete(rule);
                  if (scope.rules.size === 0) {
                    scopes.splice(s, 1);
                  }
                }
                break;
              }
            }
          }
          break;
        case 'disable-next-line':
          if (affectsRule) {
            lineScopedUntil = Math.max(lineScopedUntil, Math.min(i + 1, lines.length - 1));
          }
          break;
        case 'disable-next-n-lines':
          if (affectsRule) {
            lineScopedUntil = Math.max(lineScopedUntil, Math.min(i + marker.count, lines.length - 1));
          }
          break;
      }
      continue;
    }

    result[i] = i <= lineScopedUntil || scopes.some((scope) => scopeDisables(scope, alias));
  }

  return result;
}

export function replaceDisabledLines(text: string, placeholder: string, alias: string, knownAliases: Set<string>): [string[], string] {
  const protectedLines = getProtectedLines(text, alias, knownAliases);
  if (!protectedLines.includes(true)) {
    return [[], text];
  }

  const lines = text.split('\n');
  const output: string[] = [];
  const replacedValues: string[] = [];
  let i = 0;
  while (i < lines.length) {
    if (!protectedLines[i]) {
      output.push(lines[i++]);
      continue;
    }

    const start = i;
    while (i < lines.length && protectedLines[i]) {
      i++;
    }

    replacedValues.push(lines.slice(start, i).join('\n'));
    output.push(placeholder);
  }

  return [replacedValues, output.join('\n')];
}
