import {getPositions, MDAstTypes} from './mdast';
import {yamlRegex} from './regex';

type Scope = Set<string> | null;

const markerRegex = /^\s*(?:<!--|%%)\s*linter-(disable-next-n-lines:\s*(\S+)|disable-next-line|disable|enable)(?:\s+([^]*?))?\s*(?:-->|%%)\s*$/i;

export function applyScopedIgnore(text: string, alias: string, func: (text: string) => string): string {
  const lines = text.split('\n');
  const ignoredLines = new Set<number>();
  const protectedLines = getProtectedLines(text);
  const scopes: {rules: Scope, end?: number}[] = [];

  const normalized = (value: string): Set<string> => new Set(value.split(',').map((item) => item.trim().toLowerCase()).filter((item) => item.length > 0));
  const knownAlias = (value: string): boolean => value === 'all' || value === alias.toLowerCase();
  const rulesFor = (value: string | undefined): Scope | undefined => {
    if (!value || value.trim() === '') return null;
    const result = normalized(value);
    for (const rule of [...result]) if (!knownAlias(rule)) result.delete(rule);
    return result.size > 0 ? result : undefined;
  };
  const disables = (scope: Scope): boolean => scope === null || (scope?.has(alias.toLowerCase()) ?? false);

  for (let lineNumber = 0; lineNumber < lines.length; lineNumber++) {
    const match = protectedLines.has(lineNumber) ? null : lines[lineNumber].match(markerRegex);
    const active = scopes.some((scope) => disables(scope.rules) && (scope.end === undefined || lineNumber <= scope.end));
    if (active) ignoredLines.add(lineNumber);
    if (!match) continue;
    const kind = match[1].toLowerCase();
    const ruleScope = rulesFor(match[3]);
    if (kind === 'disable' && ruleScope !== undefined) {
      scopes.push({rules: ruleScope});
    } else if (kind === 'enable') {
      if (ruleScope === null) scopes.pop();
      else if (ruleScope !== undefined) {
        for (const rule of ruleScope) {
          for (let i = scopes.length - 1; i >= 0; i--) {
            if (scopes[i].rules === null || scopes[i].rules.has(rule)) {
              if (scopes[i].rules !== null) scopes[i].rules.delete(rule);
              break;
            }
          }
        }
        while (scopes.length && scopes[scopes.length - 1].rules !== null && scopes[scopes.length - 1].rules.size === 0) scopes.pop();
      }
    } else if (kind === 'disable-next-line' && ruleScope !== undefined && lineNumber + 1 < lines.length) {
      if (disables(ruleScope)) ignoredLines.add(lineNumber + 1);
    } else if (kind.startsWith('disable-next-n-lines:')) {
      const count = Number(kind.substring(kind.indexOf(':') + 1));
      if (/^[1-9]\d*$/.test(match[1].split(':')[1].trim()) && ruleScope !== undefined) {
        for (let i = lineNumber + 1; i <= Math.min(lines.length - 1, lineNumber + count); i++) if (disables(ruleScope)) ignoredLines.add(i);
      }
    }
  }

  const protectedText = lines.map((line, index) => ignoredLines.has(index) || protectedLines.has(index) ? `\u0000${index}\u0000` : line).join('\n');
  const result = func(protectedText);
  return result.replace(/\u0000(\d+)\u0000/g, (_, index: string) => lines[Number(index)]);
}

function getProtectedLines(text: string): Set<number> {
  const result = new Set<number>();
  const addPositions = (type: MDAstTypes) => getPositions(type, text).forEach((position) => {
    const start = text.substring(0, position.start.offset).split('\n').length - 1;
    const end = text.substring(0, position.end.offset).split('\n').length - 1;
    for (let line = start; line <= end; line++) result.add(line);
  });
  addPositions(MDAstTypes.Code);
  addPositions(MDAstTypes.InlineCode);
  addPositions(MDAstTypes.Math);
  addPositions(MDAstTypes.InlineMath);
  const yaml = text.match(yamlRegex);
  if (yaml) {
    const endLine = text.substring(0, yaml.index + yaml[0].length).split('\n').length - 1;
    for (let line = 0; line <= endLine; line++) result.add(line);
  }
  return result;
}
