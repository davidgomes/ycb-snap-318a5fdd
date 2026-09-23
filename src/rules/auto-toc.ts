import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';
import {getPositions, MDAstTypes} from '../utils/mdast';
import {yamlRegex} from '../utils/regex';

type ListStyle = 'bullet' | 'number';
type OrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle?: ListStyle = 'bullet';
  bulletMarker?: string = '-';
  orderedListStyle?: OrderedListStyle = 'always-one';
  indentSize?: Number = 2;
  minLevel?: Number = 2;
  maxLevel?: Number = 6;
  title?: string = '';
  useExplicitIds?: boolean = false;
  stripFormattingInToc?: boolean = false;
  excludeHeadings?: string[] = [];
}

type ResolvedOptions = {
  listStyle: ListStyle,
  bulletMarker: string,
  orderedListStyle: OrderedListStyle,
  indentSize: number,
  minLevel: number,
  maxLevel: number,
  title: string,
  useExplicitIds: boolean,
  stripFormattingInToc: boolean,
  excludeHeadings: string[],
};

type HeadingInfo = {
  level: number,
  content: string,
  start: number,
  end: number,
};

const TOC_START = /<!--\s*toc\s*-->/gi;
const TOC_END = /<!--\s*\/\s*toc\s*-->/gi;

@RuleBuilder.register
export default class AutoToc extends RuleBuilder<AutoTocOptions> {
  constructor() {
    super({
      nameKey: 'rules.auto-toc.name',
      descriptionKey: 'rules.auto-toc.description',
      type: RuleType.HEADING,
    });
  }
  get OptionsClass(): new () => AutoTocOptions {
    return AutoTocOptions;
  }
  apply(text: string, options: AutoTocOptions = {}): string {
    if (!/<!--\s*toc\s*-->/i.test(text)) {
      return text;
    }

    const usesCRLF = text.includes('\r\n');
    const normalized = usesCRLF ? text.replace(/\r\n/g, '\n') : text;
    const updated = applyAutoToc(normalized, resolveOptions(options));
    if (updated === normalized) {
      return text;
    }

    return usesCRLF ? updated.replace(/\n/g, '\r\n') : updated;
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Leaves the note unchanged when no toc marker is present',
        before: dedent`
          # Title
          ## Section
        `,
        after: dedent`
          # Title
          ## Section
        `,
      }),
      new ExampleBuilder({
        description: 'Creates a nested bullet list between the toc markers for ATX headings from level 2 through 6',
        before: dedent`
          # Title

          <!-- toc -->

          ## Section
          ### Subsection
        `,
        after: dedent`
          # Title

          <!-- toc -->

          - [Section](#section)
            - [Subsection](#subsection)

          <!-- /toc -->

          ## Section
          ### Subsection
        `,
      }),
      new ExampleBuilder({
        description: 'Numbered TOC with `title = Contents`, `listStyle = number`, and `orderedListStyle = increment` skips H1s and headings in code',
        before: dedent`
          <!-- toc -->

          # Ignored H1

          \`\`\`
          ## Ignored in code
          \`\`\`

          ## Included
          ### Nested
        `,
        after: dedent`
          <!-- toc -->

          Contents

          1. [Included](#included)
            2. [Nested](#nested)

          <!-- /toc -->

          # Ignored H1

          \`\`\`
          ## Ignored in code
          \`\`\`

          ## Included
          ### Nested
        `,
        options: {
          listStyle: 'number',
          orderedListStyle: 'increment',
          title: 'Contents',
        },
      }),
    ];
  }
  get optionBuilders(): OptionBuilderBase<AutoTocOptions>[] {
    return [
      new DropdownOptionBuilder<AutoTocOptions, ListStyle>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.list-style.name',
        descriptionKey: 'rules.auto-toc.list-style.description',
        optionsKey: 'listStyle',
        records: [
          {
            value: 'bullet',
            description: 'Unordered list items',
          },
          {
            value: 'number',
            description: 'Ordered list items',
          },
        ],
      }),
      new TextOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.bullet-marker.name',
        descriptionKey: 'rules.auto-toc.bullet-marker.description',
        optionsKey: 'bulletMarker',
      }),
      new DropdownOptionBuilder<AutoTocOptions, OrderedListStyle>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.ordered-list-style.name',
        descriptionKey: 'rules.auto-toc.ordered-list-style.description',
        optionsKey: 'orderedListStyle',
        records: [
          {
            value: 'always-one',
            description: 'Every item uses 1.',
          },
          {
            value: 'increment',
            description: 'Number every item in document order, including nested items',
          },
        ],
      }),
      new NumberOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.indent-size.name',
        descriptionKey: 'rules.auto-toc.indent-size.description',
        optionsKey: 'indentSize',
      }),
      new NumberOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.min-level.name',
        descriptionKey: 'rules.auto-toc.min-level.description',
        optionsKey: 'minLevel',
      }),
      new NumberOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.max-level.name',
        descriptionKey: 'rules.auto-toc.max-level.description',
        optionsKey: 'maxLevel',
      }),
      new TextOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.title.name',
        descriptionKey: 'rules.auto-toc.title.description',
        optionsKey: 'title',
      }),
      new BooleanOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.use-explicit-ids.name',
        descriptionKey: 'rules.auto-toc.use-explicit-ids.description',
        optionsKey: 'useExplicitIds',
      }),
      new BooleanOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.strip-formatting-in-toc.name',
        descriptionKey: 'rules.auto-toc.strip-formatting-in-toc.description',
        optionsKey: 'stripFormattingInToc',
      }),
      new TextAreaOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.exclude-headings.name',
        descriptionKey: 'rules.auto-toc.exclude-headings.description',
        optionsKey: 'excludeHeadings',
      }),
    ];
  }
}

function resolveOptions(options?: AutoTocOptions): ResolvedOptions {
  const defaults = new AutoTocOptions();
  const source = Object.assign(defaults, options ?? {});

  return {
    listStyle: source.listStyle === 'number' ? 'number' : 'bullet',
    bulletMarker: typeof source.bulletMarker === 'string' && source.bulletMarker !== '' ? source.bulletMarker : '-',
    orderedListStyle: source.orderedListStyle === 'increment' ? 'increment' : 'always-one',
    indentSize: normalizeNumber(source.indentSize, 2),
    minLevel: normalizeNumber(source.minLevel, 2),
    maxLevel: normalizeNumber(source.maxLevel, 6),
    title: typeof source.title === 'string' ? source.title.replace(/\s*\n\s*/g, ' ').trim() : '',
    useExplicitIds: source.useExplicitIds === true,
    stripFormattingInToc: source.stripFormattingInToc === true,
    excludeHeadings: normalizePatterns(source.excludeHeadings),
  };
}

function normalizeNumber(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(parsed)) {
    return fallback;
  }

  return parsed;
}

function normalizePatterns(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((entry) => String(entry));
  }

  if (typeof value === 'string') {
    return value.split(/\n/);
  }

  return [];
}

function applyAutoToc(text: string, options: ResolvedOptions): string {
  const ignored = ignoredRanges(text);
  const start = findMarker(text, new RegExp(TOC_START.source, 'gi'), 0, ignored);
  if (!start) {
    return text;
  }

  const end = findMarker(text, new RegExp(TOC_END.source, 'gi'), start.index + start.length, ignored);
  const regionEnd = end ? end.index + end.length : start.index + start.length;
  const headings = collectHeadings(text, start.index, regionEnd, ignored);
  const items = renderItems(headings, options);
  const block = buildTocBlock(options.title, items);
  const before = text.slice(0, start.index);
  const after = text.slice(regionEnd);

  return spliceToc(before, block, after);
}

function findMarker(text: string, pattern: RegExp, from: number, ignored: Array<[number, number]>): {index: number, length: number} | null {
  pattern.lastIndex = from;
  let match = pattern.exec(text);
  while (match) {
    if (!isInRanges(match.index, ignored)) {
      return {index: match.index, length: match[0].length};
    }

    if (pattern.lastIndex <= match.index) {
      pattern.lastIndex = match.index + 1;
    }

    match = pattern.exec(text);
  }

  return null;
}

function ignoredRanges(text: string): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  const yaml = text.match(yamlRegex);
  if (yaml && yaml.index != null) {
    ranges.push([yaml.index, yaml.index + yaml[0].length]);
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      const start = position.start?.offset;
      const end = position.end?.offset;
      if (start == null || end == null || end <= start) {
        continue;
      }

      ranges.push([start, end]);
    }
  }

  return ranges;
}

function isInRanges(index: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => index >= start && index < end);
}

function rangesOverlap(start: number, end: number, rangeStart: number, rangeEnd: number): boolean {
  return start < rangeEnd && end > rangeStart;
}

function collectHeadings(text: string, regionStart: number, regionEnd: number, ignored: Array<[number, number]>): HeadingInfo[] {
  const headings: HeadingInfo[] = [];
  for (const position of getPositions(MDAstTypes.Heading, text)) {
    const start = position.start?.offset;
    const end = position.end?.offset;
    if (start == null || end == null || end < start) {
      continue;
    }

    if (rangesOverlap(start, end, regionStart, regionEnd)) {
      continue;
    }

    if (ignored.some(([rangeStart, rangeEnd]) => rangesOverlap(start, end, rangeStart, rangeEnd))) {
      continue;
    }

    const parsed = parseAtxLine(lineAt(text, start));
    if (!parsed) {
      continue;
    }

    headings.push({
      level: parsed.level,
      content: parsed.content,
      start,
      end,
    });
  }

  headings.sort((a, b) => a.start - b.start);
  return headings;
}

function lineAt(text: string, offset: number): string {
  const start = text.lastIndexOf('\n', Math.max(0, offset - 1)) + 1;
  const endIndex = text.indexOf('\n', offset);
  const end = endIndex === -1 ? text.length : endIndex;
  return text.slice(start, end);
}

function parseAtxLine(line: string): {level: number, content: string} | null {
  let rest = line.trim();
  while (/^>\s?/.test(rest)) {
    rest = rest.replace(/^>\s?/, '').trim();
  }

  const match = rest.match(/^(#{1,6})(?:[ \t]+(.*))?$/);
  if (!match) {
    return null;
  }

  const content = (match[2] ?? '').replace(/[ \t]+#+\s*$/, '').trim();
  return {level: match[1].length, content};
}

function renderItems(headings: HeadingInfo[], options: ResolvedOptions): string[] {
  const usedAnchors = new Set<string>();
  const items: string[] = [];
  let number = 1;
  const indentSize = Math.max(0, Math.floor(options.indentSize));

  for (const heading of headings) {
    if (heading.level < options.minLevel || heading.level > options.maxLevel) {
      continue;
    }

    const {text, explicitId} = splitExplicitId(heading.content, options.useExplicitIds);
    if (isExcluded(exclusionCandidates(heading.content, text), options.excludeHeadings)) {
      continue;
    }

    const base = explicitId !== null ? explicitId : slugifyHeading(text);
    const anchor = dedupeAnchor(base, usedAnchors);
    const display = options.stripFormattingInToc ? readableHeading(text) : text;
    const depth = Math.max(0, heading.level - options.minLevel);
    const indent = ' '.repeat(depth * indentSize);
    const marker = options.listStyle === 'number' ?
      `${options.orderedListStyle === 'increment' ? number++ : 1}.` :
      options.bulletMarker;

    items.push(`${indent}${marker} [${display}](#${anchor})`);
  }

  return items;
}

function exclusionCandidates(content: string, withoutExplicitId: string): string[] {
  if (withoutExplicitId === content) {
    return [content];
  }

  return [content, withoutExplicitId];
}

function splitExplicitId(content: string, useExplicitIds: boolean): {text: string, explicitId: string | null} {
  if (!useExplicitIds) {
    return {text: content, explicitId: null};
  }

  const match = content.match(/^(.*?)[ \t]*\{#([^{}]*)\}[ \t]*$/);
  if (!match) {
    return {text: content, explicitId: null};
  }

  return {text: match[1].trim(), explicitId: match[2].trim()};
}

function isExcluded(candidates: string[], patterns: string[]): boolean {
  for (const pattern of patterns) {
    const trimmed = pattern.trim();
    if (trimmed === '') {
      continue;
    }

    if (trimmed.length >= 2 && trimmed.startsWith('/') && trimmed.endsWith('/')) {
      try {
        const regex = new RegExp(trimmed.slice(1, -1), 'i');
        if (candidates.some((candidate) => regex.test(candidate))) {
          return true;
        }
        continue;
      } catch {
        // Invalid patterns fall through to a literal comparison.
      }
    }

    const literal = trimmed.toLowerCase();
    if (candidates.some((candidate) => candidate.toLowerCase() === literal)) {
      return true;
    }
  }

  return false;
}

function readableHeading(content: string): string {
  let text = resolveLinksToDisplayText(content);
  text = removeImageEmbeds(text);
  text = stripFormatting(text);
  text = text.replace(/[ \t]+#+\s*$/, '');
  return text.replace(/[ \t]{2,}/g, ' ').trim();
}

function slugifyHeading(content: string): string {
  let text = resolveLinksToDisplayText(content);
  text = removeImageEmbeds(text);
  text = stripFormatting(text);
  text = text.replace(/[ \t]+#+\s*$/, '');
  text = text.toLowerCase();
  text = text.replace(/ /g, '-');
  text = text.replace(/[^a-z0-9_-]/g, '');
  text = text.replace(/-+/g, '-');
  text = text.replace(/^-+|-+$/g, '');
  return text;
}

function dedupeAnchor(base: string, used: Set<string>): string {
  if (!used.has(base)) {
    used.add(base);
    return base;
  }

  let suffix = 1;
  let candidate = `${base}-${suffix}`;
  while (used.has(candidate)) {
    suffix++;
    candidate = `${base}-${suffix}`;
  }

  used.add(candidate);
  return candidate;
}

function resolveLinksToDisplayText(text: string): string {
  const withoutWiki = text.replace(/\[\[([^\]]+)\]\]/g, (match, inner: string, offset: number, source: string) => {
    if ((offset > 0 && source[offset - 1] === '!') || isEscaped(source, offset)) {
      return match;
    }

    const pipeIndex = inner.indexOf('|');
    if (pipeIndex === -1) {
      return inner.trim();
    }

    return inner.slice(pipeIndex + 1).trim();
  });

  return resolveMarkdownLinks(withoutWiki);
}

function resolveMarkdownLinks(text: string): string {
  let result = '';
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '[' && !isEscaped(text, i) && (i === 0 || text[i - 1] !== '!')) {
      const parsed = parseMarkdownLink(text, i);
      if (parsed) {
        result += parsed.label;
        i = parsed.end - 1;
        continue;
      }
    }

    result += text[i];
  }

  return result;
}

type ParsedLink = {label: string, end: number, kind: 'inline' | 'reference'};

function parseMarkdownLink(text: string, start: number): ParsedLink | null {
  if (text[start] !== '[') {
    return null;
  }

  let i = start + 1;
  let depth = 1;
  const labelStart = i;
  while (i < text.length && depth > 0) {
    if (text[i] === '\\') {
      i += 2;
      continue;
    }

    if (text[i] === '[') {
      depth++;
    } else if (text[i] === ']') {
      depth--;
    }

    if (depth > 0) {
      i++;
    }
  }

  if (i >= text.length || text[i] !== ']') {
    return null;
  }

  const label = text.slice(labelStart, i);
  i++;
  if (text[i] === '(') {
    let parenDepth = 1;
    i++;
    while (i < text.length && parenDepth > 0) {
      if (text[i] === '\\') {
        i += 2;
        continue;
      }

      if (text[i] === '(') {
        parenDepth++;
      } else if (text[i] === ')') {
        parenDepth--;
      }

      if (parenDepth > 0) {
        i++;
      }
    }

    if (i >= text.length || text[i] !== ')') {
      return null;
    }

    return {label, end: i + 1, kind: 'inline'};
  }

  if (text[i] === '[') {
    i++;
    while (i < text.length && text[i] !== ']') {
      if (text[i] === '\\') {
        i += 2;
        continue;
      }

      i++;
    }

    if (i >= text.length || text[i] !== ']') {
      return null;
    }

    return {label, end: i + 1, kind: 'reference'};
  }

  return null;
}

function removeImageEmbeds(text: string): string {
  let result = '';
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '!' && text[i + 1] === '[' && !isEscaped(text, i)) {
      const wikiEnd = wikiEmbedEnd(text, i);
      if (wikiEnd != null) {
        i = wikiEnd - 1;
        continue;
      }

      const parsed = parseMarkdownLink(text, i + 1);
      if (parsed && parsed.kind === 'inline') {
        i = parsed.end - 1;
        continue;
      }
    }

    result += text[i];
  }

  return result;
}

function wikiEmbedEnd(text: string, bangIndex: number): number | null {
  if (!text.startsWith('![[', bangIndex)) {
    return null;
  }

  const close = text.indexOf(']]', bangIndex + 3);
  if (close === -1 || text.slice(bangIndex, close).includes('\n')) {
    return null;
  }

  return close + 2;
}

function isEscaped(text: string, index: number): boolean {
  let slashCount = 0;
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) {
    slashCount++;
  }

  return slashCount % 2 === 1;
}

function stripFormatting(text: string): string {
  const protectedBits: string[] = [];
  const protect = (value: string): string => {
    const token = `\uE000${protectedBits.length}\uE001`;
    protectedBits.push(value);
    return token;
  };

  let result = text.replace(/\\([\\`*_{}[\]()#+\-.!|~=])/g, (_match, char: string) => protect(char));
  result = result.replace(/`([^`]*)`/g, (_match, inner: string) => protect(inner));
  result = result.replace(/<[^>\n]+>/g, '');
  result = result.replace(/==([^=\n]+)==/g, '$1');
  result = result.replace(/~~([^~\n]+)~~/g, '$1');

  for (let pass = 0; pass < 10; pass++) {
    const previous = result;
    result = result.replace(/\*\*\*([^*\n]+)\*\*\*/g, '$1');
    result = result.replace(/___([^_\n]+)___/g, '$1');
    result = result.replace(/\*\*([^*\n]+)\*\*/g, '$1');
    result = result.replace(/__([^_\n]+)__/g, '$1');
    result = result.replace(/\*([^*\n]+)\*/g, '$1');
    result = result.replace(/(^|[\s([])_(?!\s)([^_\n]*?\S)_(?=$|[\s).,!?:;])/g, '$1$2');
    if (result === previous) {
      break;
    }
  }

  return result.replace(/\uE000(\d+)\uE001/g, (_match, index: string) => protectedBits[Number(index)] ?? '');
}

function buildTocBlock(title: string, items: string[]): string {
  const lines = ['<!-- toc -->', ''];
  if (title !== '') {
    lines.push(title, '');
  }

  if (items.length > 0) {
    lines.push(...items, '');
  }

  lines.push('<!-- /toc -->');
  return lines.join('\n');
}

function spliceToc(before: string, block: string, after: string): string {
  let prefix = before;
  if (prefix.length > 0 && !prefix.endsWith('\n')) {
    prefix += '\n';
  }

  const suffix = consumeRemainderOfMarkerLine(after);
  if (suffix.length === 0) {
    return `${prefix}${block}\n\n`;
  }

  if (suffix.startsWith('\n')) {
    return `${prefix}${block}\n${suffix}`;
  }

  return `${prefix}${block}\n\n${suffix}`;
}

function consumeRemainderOfMarkerLine(after: string): string {
  const match = /^(.*)(\n)?/.exec(after);
  if (!match) {
    return after;
  }

  const lineContent = match[1];
  const lineBreak = match[2] ?? '';
  if (lineContent.trim() !== '') {
    return after;
  }

  return after.slice(lineContent.length + lineBreak.length);
}
