import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';
import {MDAstTypes, getPositions} from '../utils/mdast';
import {yamlRegex} from '../utils/regex';

type TocListStyle = 'bullet' | 'number';
type TocOrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle?: TocListStyle = 'bullet';
  bulletMarker?: string = '-';
  orderedListStyle?: TocOrderedListStyle = 'always-one';
  indentSize?: Number = 2;
  minLevel?: Number = 2;
  maxLevel?: Number = 6;
  title?: string = '';
  useExplicitIds?: boolean = false;
  stripFormattingInToc?: boolean = false;
  excludeHeadings?: string[] = [];
}

type Range = {start: number, end: number};

type TocHeading = {
  level: number,
  text: string,
};

const TOC_START = /<!--\s*toc\s*-->/gi;
const TOC_END = /<!--\s*\/\s*toc\s*-->/gi;
const ATX_HEADING = /^( {0,3})(#{1,6})[ \t]+([^\n]*)$/gm;
const EXPLICIT_ID = /^(.*?)(?:\s*\{#([^{}]+)\})\s*$/;
const REGEX_PATTERN = /^\/([\s\S]+)\/([gimsuy]*)$/;

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
  apply(text: string, options: AutoTocOptions): string {
    const ignored = getIgnoredRanges(text);
    const region = findTocRegion(text, ignored);
    if (!region) {
      return text;
    }

    const headings = collectHeadings(text, ignored, region.exclusion);
    const block = buildTocBlock(headings, options);
    const prefix = text.slice(0, region.start).replace(/(^|\n)[ \t]*$/, '$1');
    const suffixStart = region.end < 0 ? region.startEnd : region.endEnd;
    const suffix = text.slice(suffixStart).replace(/^[ \t]*/, '').replace(/^(?:\r?\n)+/, '');

    if (suffix.length === 0) {
      return prefix + block + '\n\n';
    }

    return prefix + block + '\n\n' + suffix;
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Inserts a table of contents for ATX headings when only the start marker is present, skipping heading level 1 by default',
        before: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          ## Section One
          ### Details
          ## Section Two
        `,
        after: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          - [Section One](#section-one)
            - [Details](#details)
          - [Section Two](#section-two)
          ${''}
          <!-- /toc -->
          ${''}
          ## Section One
          ### Details
          ## Section Two
        `,
      }),
      new ExampleBuilder({
        description: 'Replaces an existing table of contents and ignores headings in YAML, code blocks, and math blocks',
        before: dedent`
          ---
          # not a heading
          title: Note
          ---
          ${''}
          <!-- TOC -->
          - [Stale](#stale)
          <!-- /TOC -->
          ${''}
          ## Kept
          ${''}
          \`\`\`
          # Comment
          ## Also a comment
          \`\`\`
          ${''}
          $$
          # x
          $$
          ${''}
          ## After Math
        `,
        after: dedent`
          ---
          # not a heading
          title: Note
          ---
          ${''}
          <!-- toc -->
          ${''}
          - [Kept](#kept)
          - [After Math](#after-math)
          ${''}
          <!-- /toc -->
          ${''}
          ## Kept
          ${''}
          \`\`\`
          # Comment
          ## Also a comment
          \`\`\`
          ${''}
          $$
          # x
          $$
          ${''}
          ## After Math
        `,
      }),
      new ExampleBuilder({
        description: 'With `List Style = number`, `Ordered List Style = increment`, `Title = Contents`, and `Strip Formatting in TOC = true`',
        before: dedent`
          <!-- toc -->
          ${''}
          ## **Bold** and [[Page|Alias]]
          ### Sub
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          Contents
          ${''}
          1. [Bold and Alias](#bold-and-alias)
            2. [Sub](#sub)
          ${''}
          <!-- /toc -->
          ${''}
          ## **Bold** and [[Page|Alias]]
          ### Sub
        `,
        options: {
          listStyle: 'number',
          orderedListStyle: 'increment',
          title: 'Contents',
          stripFormattingInToc: true,
        },
      }),
      new ExampleBuilder({
        description: 'With `Use Explicit IDs = true`, a trailing {#id} is the anchor and duplicate anchors gain -1, -2, ...',
        before: dedent`
          <!-- toc -->
          ${''}
          ## Intro {#custom}
          ## Intro
          ## Intro
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          - [Intro](#custom)
          - [Intro](#intro)
          - [Intro](#intro-1)
          ${''}
          <!-- /toc -->
          ${''}
          ## Intro {#custom}
          ## Intro
          ## Intro
        `,
        options: {
          useExplicitIds: true,
        },
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
            description: 'Bulleted list items',
          },
          {
            value: 'number',
            description: 'Numbered list items',
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
            description: 'Every item is numbered 1',
          },
          {
            value: 'increment',
            description: 'Numbers increment across all items',
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

function toInt(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' || typeof value === 'string' || typeof value === 'object' ? Number(value) : NaN;
  if (!Number.isFinite(parsed)) {
    return fallback;
  }

  return Math.floor(parsed);
}

function normalizeExcludePatterns(value: string[] | string | undefined): string[] {
  const raw = Array.isArray(value) ? value : typeof value === 'string' ? value.split('\n') : [];
  return raw.map((pattern) => pattern.trim()).filter((pattern) => pattern.length > 0);
}

function collectMatches(text: string, regex: RegExp): RegExpExecArray[] {
  const flags = regex.flags.includes('g') ? regex.flags : regex.flags + 'g';
  const expression = new RegExp(regex.source, flags);
  const matches: RegExpExecArray[] = [];
  let match: RegExpExecArray | null;
  while ((match = expression.exec(text)) !== null) {
    matches.push(match);
    if (match[0].length === 0) {
      expression.lastIndex++;
    }
  }

  return matches;
}

function isInsideRanges(index: number, ranges: Range[]): boolean {
  for (const range of ranges) {
    if (index >= range.start && index < range.end) {
      return true;
    }
  }

  return false;
}

function pushPositionRanges(text: string, type: MDAstTypes, ranges: Range[]) {
  for (const position of getPositions(type, text)) {
    if (position?.start?.offset == null || position.end?.offset == null) {
      continue;
    }

    ranges.push({start: position.start.offset, end: position.end.offset});
  }
}

function getIgnoredRanges(text: string): Range[] {
  const ranges: Range[] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch && yamlMatch.index === 0) {
    ranges.push({start: 0, end: yamlMatch.index + yamlMatch[0].length});
  }

  pushPositionRanges(text, MDAstTypes.Code, ranges);
  pushPositionRanges(text, MDAstTypes.Math, ranges);

  return ranges;
}

function findTocRegion(text: string, ignored: Range[]): {start: number, startEnd: number, end: number, endEnd: number, exclusion: Range} | null {
  const starts = collectMatches(text, TOC_START).filter((match) => !isInsideRanges(match.index, ignored));
  if (starts.length === 0) {
    return null;
  }

  const start = starts[0];
  const startEnd = start.index + start[0].length;
  const ends = collectMatches(text, TOC_END).filter((match) => match.index >= startEnd && !isInsideRanges(match.index, ignored));
  if (ends.length === 0) {
    return {
      start: start.index,
      startEnd,
      end: -1,
      endEnd: startEnd,
      exclusion: {start: startEnd, end: startEnd},
    };
  }

  const end = ends[0];
  return {
    start: start.index,
    startEnd,
    end: end.index,
    endEnd: end.index + end[0].length,
    exclusion: {start: start.index, end: end.index + end[0].length},
  };
}

function collectHeadings(text: string, ignored: Range[], exclusion: Range): TocHeading[] {
  const headings: TocHeading[] = [];
  for (const match of collectMatches(text, ATX_HEADING)) {
    if (isInsideRanges(match.index, ignored) || isInsideRanges(match.index, [exclusion])) {
      continue;
    }

    const level = match[2].length;
    const textContent = match[3].replace(/\r$/, '').replace(/[ \t]+#+\s*$/, '').trim();
    if (textContent.length === 0 || level < 1 || level > 6) {
      continue;
    }

    headings.push({level, text: textContent});
  }

  return headings;
}

function removeImages(text: string): string {
  return text
      .replace(/!\[\[[^\]]*\]\]/g, '')
      .replace(/!\[[^\]]*\]\([^)\n]*\)/g, '');
}

function resolveLinks(text: string): string {
  return text
      .replace(/\[\[([^\]|\n]+)(?:\|([^\]\n]+))?\]\]/g, (_match, target: string, alias?: string) => alias != null ? alias : target)
      .replace(/\[([^\n\]]*)\]\([^)\n]*\)/g, '$1')
      .replace(/\[([^\n\]]+)\]\[[^\n\]]*\]/g, '$1');
}

function stripFormatting(text: string): string {
  let current = text;
  for (let i = 0; i < 10; i++) {
    const next = current
        .replace(/`([^`\n]+)`/g, '$1')
        .replace(/<\/?[A-Za-z][^>\n]*>/g, '')
        .replace(/==([^=\n]+)==/g, '$1')
        .replace(/~~([^~\n]+)~~/g, '$1')
        .replace(/\*\*\*([^*]+)\*\*\*/g, '$1')
        .replace(/___([^_]+)___/g, '$1')
        .replace(/\*\*([^*]+)\*\*/g, '$1')
        .replace(/__([^_]+)__/g, '$1')
        .replace(/\*([^*]+)\*/g, '$1')
        .replace(/(^|\s)_([^_\n]+)_(\s|$)/g, '$1$2$3');
    if (next === current) {
      break;
    }

    current = next;
  }

  return current;
}

function slugify(text: string): string {
  const withoutTrailingHashes = stripFormatting(text).replace(/#+\s*$/, '');
  return withoutTrailingHashes
      .toLowerCase()
      .replace(/ /g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-{2,}/g, '-')
      .replace(/^-+|-+$/g, '');
}

function headingIsExcluded(headingText: string, patterns: string[]): boolean {
  const candidate = headingText.trim();
  const lower = candidate.toLowerCase();
  for (const pattern of patterns) {
    const regexBody = pattern.match(REGEX_PATTERN);
    if (regexBody) {
      try {
        if (new RegExp(regexBody[1], 'i').test(candidate)) {
          return true;
        }
      } catch {
        if (lower === pattern.toLowerCase()) {
          return true;
        }
      }
      continue;
    }

    if (lower === pattern.toLowerCase()) {
      return true;
    }
  }

  return false;
}

function escapeLinkLabel(text: string): string {
  return text.replace(/[\\[\]]/g, '\\$&');
}

function uniqueAnchor(base: string, used: Set<string>): string {
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

function prepareHeading(headingText: string, useExplicitIds: boolean, stripFormattingInToc: boolean): {display: string, baseAnchor: string} {
  let text = headingText.trim();
  let explicitId = '';
  if (useExplicitIds) {
    const match = text.match(EXPLICIT_ID);
    if (match && match[2].trim() !== '') {
      explicitId = match[2].trim();
      text = match[1].trim();
    }
  }

  text = resolveLinks(removeImages(text)).trim();
  const display = (stripFormattingInToc ? stripFormatting(text) : text).trim();
  const baseAnchor = explicitId !== '' ? explicitId : slugify(text);

  return {display, baseAnchor};
}

function buildTocBlock(headings: TocHeading[], options: AutoTocOptions): string {
  const listStyle = options.listStyle === 'number' ? 'number' : 'bullet';
  const orderedListStyle = options.orderedListStyle === 'increment' ? 'increment' : 'always-one';
  const bulletMarker = options.bulletMarker == null || options.bulletMarker === '' ? '-' : String(options.bulletMarker);
  const indentSize = Math.max(0, toInt(options.indentSize, 2));
  const minLevel = toInt(options.minLevel, 2);
  const maxLevel = toInt(options.maxLevel, 6);
  const title = options.title == null ? '' : String(options.title);
  const useExplicitIds = options.useExplicitIds === true;
  const stripFormattingInToc = options.stripFormattingInToc === true;
  const excludePatterns = normalizeExcludePatterns(options.excludeHeadings);
  const usedAnchors = new Set<string>();
  const items: string[] = [];
  let count = 0;

  for (const heading of headings) {
    if (heading.level < minLevel || heading.level > maxLevel) {
      continue;
    }

    if (headingIsExcluded(heading.text, excludePatterns)) {
      continue;
    }

    const prepared = prepareHeading(heading.text, useExplicitIds, stripFormattingInToc);
    if (prepared.display.length === 0 && prepared.baseAnchor.length === 0) {
      continue;
    }

    const anchor = uniqueAnchor(prepared.baseAnchor, usedAnchors);
    const indent = ' '.repeat(Math.max(0, heading.level - minLevel) * indentSize);
    const marker = listStyle === 'number' ?
      `${orderedListStyle === 'increment' ? ++count : 1}. ` :
      `${bulletMarker} `;
    const label = prepared.display.length === 0 ? anchor : prepared.display;
    items.push(`${indent}${marker}[${escapeLinkLabel(label)}](#${anchor})`);
  }

  const parts: string[] = ['<!-- toc -->', ''];
  if (title.trim() !== '') {
    parts.push(title, '');
  }
  if (items.length > 0) {
    parts.push(items.join('\n'), '');
  }
  parts.push('<!-- /toc -->');

  return parts.join('\n');
}
