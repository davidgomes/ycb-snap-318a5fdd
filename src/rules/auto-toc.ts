import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';
import {IgnoreTypes} from '../utils/ignore-types';
import {allHeadersRegex, yamlRegex} from '../utils/regex';
import {getPositions, MDAstTypes} from '../utils/mdast';

type TocListStyle = 'bullet' | 'number';
type TocOrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle: TocListStyle = 'bullet';
  bulletMarker: string = '-';
  orderedListStyle: TocOrderedListStyle = 'always-one';
  indentSize: Number = 2;
  minLevel: Number = 2;
  maxLevel: Number = 6;
  title: string = '';
  useExplicitIds: boolean = false;
  stripFormattingInToc: boolean = false;
  excludeHeadings: string[] = [];
}

@RuleBuilder.register
export default class AutoToc extends RuleBuilder<AutoTocOptions> {
  constructor() {
    super({
      nameKey: 'rules.auto-toc.name',
      descriptionKey: 'rules.auto-toc.description',
      type: RuleType.HEADING,
      // Run after heading rules such as capitalize headings so anchors match the final heading text.
      hasSpecialExecutionOrder: true,
      ruleIgnoreTypes: [IgnoreTypes.code, IgnoreTypes.math, IgnoreTypes.yaml],
    });
  }
  get OptionsClass(): new () => AutoTocOptions {
    return AutoTocOptions;
  }
  apply(text: string, options: AutoTocOptions): string {
    const listStyle: TocListStyle = options.listStyle === 'number' ? 'number' : 'bullet';
    const bulletMarker = options.bulletMarker ?? '-';
    const orderedListStyle: TocOrderedListStyle = options.orderedListStyle === 'increment' ? 'increment' : 'always-one';
    const indentSize = Math.max(0, toNumber(options.indentSize, 2));
    const minLevel = toNumber(options.minLevel, 2);
    const maxLevel = toNumber(options.maxLevel, 6);
    const title = options.title ?? '';
    const useExplicitIds = options.useExplicitIds === true;
    const stripFormattingInToc = options.stripFormattingInToc === true;
    const excludeHeadings = normalizeExcludeHeadings(options.excludeHeadings);

    const ignoredRanges = getIgnoredRanges(text);
    const startMatch = findMarker(text, TOC_START, 0, ignoredRanges);
    if (!startMatch) {
      return text;
    }

    const startIndex = startMatch.index;
    const startMarker = startMatch[0];
    const afterStart = startIndex + startMarker.length;
    const endMatch = findMarker(text, TOC_END, afterStart, ignoredRanges);

    let regionEnd = afterStart;
    let endMarker = '<!-- /toc -->';
    let suffixStart = consumeMarkerLineEnding(text, afterStart);
    if (endMatch) {
      regionEnd = endMatch.index;
      endMarker = endMatch[0];
      suffixStart = consumeMarkerLineEnding(text, endMatch.index + endMarker.length);
    }

    const headings = collectHeadings(text, startIndex, regionEnd, ignoredRanges, useExplicitIds);
    const items: string[] = [];
    const usedAnchors = new Set<string>();
    let orderedIndex = 1;
    for (const heading of headings) {
      if (heading.level < minLevel || heading.level > maxLevel || isExcludedHeading(heading, excludeHeadings)) {
        continue;
      }

      const anchor = dedupeAnchor(heading.baseAnchor, usedAnchors);
      const display = stripFormattingInToc ? heading.strippedText : heading.rawText;
      const indent = ' '.repeat((heading.level - minLevel) * indentSize);
      const marker = listStyle === 'number' ?
        `${orderedListStyle === 'increment' ? orderedIndex : 1}.` :
        bulletMarker;
      const spacer = marker.endsWith(' ') ? '' : ' ';
      items.push(`${indent}${marker}${spacer}[${display}](#${anchor})`);
      orderedIndex++;
    }

    const block = buildTocBlock(startMarker, endMarker, title, items);
    const before = text.slice(0, startIndex);
    const suffix = text.slice(suffixStart);
    return before + block + ensureBlankLineAfter(suffix);
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Text without a TOC marker is left unchanged',
        before: dedent`
          # Title
          ${''}
          ## Section
        `,
        after: dedent`
          # Title
          ${''}
          ## Section
        `,
      }),
      new ExampleBuilder({
        description: 'Inserts a bullet TOC for ATX headings between levels 2 and 6 and adds a closing marker when it is missing',
        before: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          ## Section
          ${''}
          ### Detail
        `,
        after: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          - [Section](#section)
            - [Detail](#detail)
          ${''}
          <!-- /toc -->
          ${''}
          ## Section
          ${''}
          ### Detail
        `,
      }),
      new ExampleBuilder({
        description: 'Replaces the first TOC region and keeps headings inside that region out of the list',
        before: dedent`
          <!-- TOC -->
          ${''}
          - [Old](#old)
          ${''}
          ## Inside the old toc
          ${''}
          <!-- /TOC -->
          ${''}
          ## Kept
        `,
        after: dedent`
          <!-- TOC -->
          ${''}
          - [Kept](#kept)
          ${''}
          <!-- /TOC -->
          ${''}
          ## Kept
        `,
      }),
    ];
  }
  get optionBuilders(): OptionBuilderBase<AutoTocOptions>[] {
    return [
      new DropdownOptionBuilder<AutoTocOptions, TocListStyle>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.list-style.name',
        descriptionKey: 'rules.auto-toc.list-style.description',
        optionsKey: 'listStyle',
        records: [
          {
            value: 'bullet',
            description: 'Uses an unordered list for the table of contents',
          },
          {
            value: 'number',
            description: 'Uses an ordered list for the table of contents',
          },
        ],
      }),
      new TextOptionBuilder({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.bullet-marker.name',
        descriptionKey: 'rules.auto-toc.bullet-marker.description',
        optionsKey: 'bulletMarker',
      }),
      new DropdownOptionBuilder<AutoTocOptions, TocOrderedListStyle>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.ordered-list-style.name',
        descriptionKey: 'rules.auto-toc.ordered-list-style.description',
        optionsKey: 'orderedListStyle',
        records: [
          {
            value: 'always-one',
            description: 'Uses 1. for every ordered list item',
          },
          {
            value: 'increment',
            description: 'Increments the ordered list marker across every item',
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

const TOC_START = /<!--\s*toc\s*-->/i;
const TOC_END = /<!--\s*\/\s*toc\s*-->/i;

type TocHeading = {
  level: number,
  rawText: string,
  strippedText: string,
  matchText: string,
  baseAnchor: string,
};

function toNumber(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' || typeof value === 'bigint' ? Number(value) : Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function normalizeExcludeHeadings(value: string[] | string | undefined): string[] {
  if (value == null) {
    return [];
  }

  const parts = Array.isArray(value) ? value : String(value).split(/\r?\n/);
  return parts.map((part) => part.trim()).filter((part) => part !== '');
}

function getIgnoredRanges(text: string): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  const yaml = text.match(yamlRegex);
  if (yaml && yaml.index != null) {
    ranges.push([yaml.index, yaml.index + yaml[0].length]);
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      if (position?.start?.offset != null && position?.end?.offset != null) {
        ranges.push([position.start.offset, position.end.offset]);
      }
    }
  }

  return ranges;
}

function isInsideRange(index: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => index >= start && index < end);
}

function findMarker(text: string, regex: RegExp, from: number, ranges: Array<[number, number]>): RegExpExecArray | null {
  const flags = regex.flags.includes('g') ? regex.flags : regex.flags + 'g';
  const search = new RegExp(regex.source, flags);
  search.lastIndex = from;
  let match = search.exec(text);
  while (match) {
    if (!isInsideRange(match.index, ranges)) {
      return match;
    }

    if (search.lastIndex === match.index) {
      search.lastIndex++;
    }
    match = search.exec(text);
  }

  return null;
}

function consumeMarkerLineEnding(text: string, index: number): number {
  let cursor = index;
  while (cursor < text.length && (text[cursor] === ' ' || text[cursor] === '\t')) {
    cursor++;
  }

  if (text[cursor] === '\r') {
    cursor++;
  }
  if (text[cursor] === '\n') {
    return cursor + 1;
  }

  return index;
}

function collectHeadings(text: string, regionStart: number, regionEnd: number, ignoredRanges: Array<[number, number]>, useExplicitIds: boolean): TocHeading[] {
  const headingRegex = new RegExp(allHeadersRegex.source, 'gm');
  const headings: TocHeading[] = [];
  let match = headingRegex.exec(text);
  while (match) {
    const index = match.index;
    const level = match[2].length;
    const inTocRegion = index >= regionStart && index < regionEnd;
    if (!inTocRegion && !isInsideRange(index, ignoredRanges)) {
      headings.push(describeHeading(level, (match[4] ?? '').trim(), useExplicitIds));
    }

    match = headingRegex.exec(text);
  }

  return headings;
}

function describeHeading(level: number, matchText: string, useExplicitIds: boolean): TocHeading {
  const explicit = useExplicitIds ? splitExplicitId(matchText) : null;
  const contentText = explicit?.id ? explicit.text : matchText;
  const source = anchorSource(contentText);
  return {
    level,
    rawText: contentText,
    strippedText: collapseWhitespace(source),
    matchText,
    baseAnchor: explicit?.id ?? slugify(source),
  };
}

function splitExplicitId(rawText: string): {text: string, id: string | null} {
  const match = rawText.match(/^(.*?)(?:\s*\{#([^{}]*)\}\s*)$/);
  if (!match) {
    return {text: rawText, id: null};
  }

  const id = match[2].trim();
  if (id === '') {
    return {text: rawText, id: null};
  }

  return {text: match[1].trim(), id};
}

function anchorSource(text: string): string {
  return stripFormatting(resolveLinksToDisplayText(removeImageEmbeds(text)));
}

function removeImageEmbeds(text: string): string {
  return text
      .replace(/!\[\[[\s\S]*?\]\]/g, '')
      .replace(/!\[[^\]]*\]\((?:\\.|[^)])*\)/g, '')
      .replace(/!\[[^\]]*\]\[[^\]]*\]/g, '');
}

function resolveLinksToDisplayText(text: string): string {
  return text
      .replace(/\[\[([^\]]+)\]\]/g, (_match, inner: string) => {
        const pipe = inner.indexOf('|');
        return pipe === -1 ? inner : inner.slice(pipe + 1);
      })
      .replace(/\[([^\]]*)\]\((?:\\.|[^)])*\)/g, '$1')
      .replace(/\[([^\]]+)\]\[([^\]]*)\]/g, '$1')
      .replace(/<((?:https?:\/\/|mailto:)[^>\s]+)>/gi, '$1');
}

function stripFormatting(text: string): string {
  let current = text;
  let previous = '';
  while (current !== previous) {
    previous = current;
    current = current
        .replace(/\*\*\*(\S(?:.*?\S)?)\*\*\*/g, '$1')
        .replace(/___(\S(?:.*?\S)?)___/g, '$1')
        .replace(/\*\*(\S(?:.*?\S)?)\*\*/g, '$1')
        .replace(/__(\S(?:.*?\S)?)__/g, '$1')
        .replace(/(^|\s)\*(\S(?:.*?\S)?)\*(?=\s|$)/g, '$1$2')
        .replace(/(^|\s)_(\S(?:.*?\S)?)_(?=\s|$)/g, '$1$2')
        .replace(/~~(\S(?:.*?\S)?)~~/g, '$1')
        .replace(/==(\S(?:.*?\S)?)==/g, '$1')
        .replace(/`([^`]+)`/g, '$1')
        .replace(/<\/?[A-Za-z][^>\n]*>/g, '');
  }

  return current;
}

function collapseWhitespace(text: string): string {
  return text.replace(/[ \t]+/g, ' ').trim();
}

function slugify(text: string): string {
  return text
      .toLowerCase()
      .replace(/ /g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '');
}

function dedupeAnchor(base: string, usedAnchors: Set<string>): string {
  if (!usedAnchors.has(base)) {
    usedAnchors.add(base);
    return base;
  }

  let suffix = 1;
  let candidate = `${base}-${suffix}`;
  while (usedAnchors.has(candidate)) {
    suffix++;
    candidate = `${base}-${suffix}`;
  }

  usedAnchors.add(candidate);
  return candidate;
}

function isExcludedHeading(heading: TocHeading, patterns: string[]): boolean {
  const candidates = [heading.matchText, heading.rawText, heading.strippedText];
  for (const pattern of patterns) {
    if (pattern.length >= 2 && pattern.startsWith('/') && pattern.endsWith('/')) {
      try {
        const regex = new RegExp(pattern.slice(1, -1), 'i');
        if (candidates.some((candidate) => regex.test(candidate))) {
          return true;
        }
        continue;
      } catch {
        // Invalid patterns fall through to a literal comparison.
      }
    }

    const literal = pattern.toLowerCase();
    if (candidates.some((candidate) => candidate.toLowerCase() === literal)) {
      return true;
    }
  }

  return false;
}

function buildTocBlock(startMarker: string, endMarker: string, title: string, items: string[]): string {
  const lines = [startMarker, ''];
  if (title !== '') {
    lines.push(title, '');
  }

  if (items.length > 0) {
    lines.push(...items, '');
  }

  lines.push(endMarker);
  return lines.join('\n');
}

function ensureBlankLineAfter(suffix: string): string {
  const rest = suffix.replace(/^(?:\r?\n)+/, '');
  if (rest.length === 0) {
    return '\n\n';
  }

  return '\n\n' + rest;
}
