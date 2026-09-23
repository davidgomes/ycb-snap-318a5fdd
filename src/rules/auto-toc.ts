import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';
import {getPositions, MDAstTypes} from '../utils/mdast';
import {wikiLinkRegex, yamlRegex} from '../utils/regex';
import {unescapeMarkdownSpecialCharacters} from '../utils/strings';

type TocListStyle = 'bullet' | 'number';
type TocBulletMarker = '-' | '*' | '+';
type TocOrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle?: TocListStyle = 'bullet';
  bulletMarker?: TocBulletMarker = '-';
  orderedListStyle?: TocOrderedListStyle = 'always-one';
  indentSize?: Number = 2;
  minLevel?: Number = 2;
  maxLevel?: Number = 6;
  title?: string = '';
  useExplicitIds?: boolean = false;
  stripFormattingInToc?: boolean = false;
  excludeHeadings?: string[] = [];
}

type TextRange = {start: number, end: number};

type TocEntry = {
  level: number,
  label: string,
  anchor: string,
};

const tocStartMarkerRegex = /<!--\s*toc\s*-->/gi;
const tocEndMarkerRegex = /<!--\s*\/\s*toc\s*-->/gi;
const defaultTocEndMarker = '<!-- /toc -->';

const atxHeadingRegex = /^ {0,3}(#{1,6})(?:[ \t]+([^\n]*?))?[ \t]*$/gm;
const closingHeadingSequenceRegex = /(?:^|[ \t]+)#+$/;
const explicitIdRegex = /[ \t]*\{#([^\s{}]+)\}$/;

// lookbehinds are avoided since older iOS versions fail to parse them, so the preceding character is captured instead
const wikiImageEmbedRegex = /!\[\[[^\]\n]*\]\]/g;
const markdownImageRegex = /!\[[^\]\n]*\]\((?:[^()\n]|\([^()\n]*\))*\)/g;
const markdownLinkRegex = /(^|[^\\])\[([^\]\n]*)\]\((?:[^()\n]|\([^()\n]*\))*\)/g;
const codeSpanRegex = /(^|[^`\\])(`+)([^\n]*?[^`\n])\2(?!`)/g;
const htmlTagRegex = /<\/?[A-Za-z][^<>\n]*>/g;
const emphasisRegexes = [
  /(^|[^\\])\*\*(?=\S)(.*?[^\s\\])\*\*/g,
  /(^|[^\p{L}\p{N}\\])__(?=\S)(.*?[^\s\\])__(?![\p{L}\p{N}])/gu,
  /(^|[^\\])\*(?=[^\s*])(.*?[^\s\\*])\*/g,
  /(^|[^\p{L}\p{N}\\_])_(?=[^\s_])(.*?[^\s\\_])_(?![\p{L}\p{N}_])/gu,
  /(^|[^\\])~~(?=\S)(.*?[^\s\\])~~/g,
  /(^|[^\\])==(?=\S)(.*?[^\s\\])==/g,
];

@RuleBuilder.register
export default class AutoToc extends RuleBuilder<AutoTocOptions> {
  constructor() {
    super({
      nameKey: 'rules.auto-toc.name',
      descriptionKey: 'rules.auto-toc.description',
      type: RuleType.CONTENT,
    });
  }
  get OptionsClass(): new () => AutoTocOptions {
    return AutoTocOptions;
  }
  apply(text: string, options: AutoTocOptions): string {
    // code, math, and YAML are skipped via ranges instead of ignore placeholders since the TOC region gets replaced wholesale
    // and dropping a placeholder that was inside it would misalign the restoration of the remaining placeholders
    const ignoredRanges = getIgnoredRanges(text);
    const startMarker = findFirstMarker(text, tocStartMarkerRegex, 0, ignoredRanges);
    if (!startMarker) {
      return text;
    }

    const startMarkerEnd = startMarker.index + startMarker[0].length;
    const endMarker = findFirstMarker(text, tocEndMarkerRegex, startMarkerEnd, ignoredRanges);
    const regionEnd = endMarker ? endMarker.index + endMarker[0].length : startMarkerEnd;

    const entries = getTocEntries(text, {start: startMarker.index, end: regionEnd}, ignoredRanges, options);

    let tocRegion = startMarker[0] + '\n\n';
    const title = (options.title ?? '').trim();
    if (title !== '') {
      tocRegion += title + '\n\n';
    }

    const tocLines = buildTocLines(entries, options);
    if (tocLines.length > 0) {
      tocRegion += tocLines.join('\n') + '\n\n';
    }

    tocRegion += endMarker ? endMarker[0] : defaultTocEndMarker;

    return text.substring(0, startMarker.index) + tocRegion + ensureBlankLineBeforeFollowingContent(text.substring(regionEnd));
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'A TOC is generated after `<!-- toc -->` and the missing `<!-- /toc -->` end marker is inserted',
        before: dedent`
          # Document Title
          ${''}
          <!-- toc -->
          ${''}
          ## Introduction
          ${''}
          ## Getting Started
          ${''}
          ### Installation
          ${''}
          ### Configuration
          ${''}
          ## FAQ
        `,
        after: dedent`
          # Document Title
          ${''}
          <!-- toc -->
          ${''}
          - [Introduction](#introduction)
          - [Getting Started](#getting-started)
            - [Installation](#installation)
            - [Configuration](#configuration)
          - [FAQ](#faq)
          ${''}
          <!-- /toc -->
          ${''}
          ## Introduction
          ${''}
          ## Getting Started
          ${''}
          ### Installation
          ${''}
          ### Configuration
          ${''}
          ## FAQ
        `,
      }),
      new ExampleBuilder({
        description: 'An existing TOC is updated, duplicate anchors get a numeric suffix, and headings in code blocks are ignored',
        before: dedent`
          <!-- toc -->
          - [Old Heading](#old-heading)
          <!-- /toc -->
          ## Setup
          ${''}
          \`\`\`markdown
          ## Not A Heading
          \`\`\`
          ${''}
          ## Setup
          ${''}
          ## What's New?
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          - [Setup](#setup)
          - [Setup](#setup-1)
          - [What's New?](#whats-new)
          ${''}
          <!-- /toc -->
          ${''}
          ## Setup
          ${''}
          \`\`\`markdown
          ## Not A Heading
          \`\`\`
          ${''}
          ## Setup
          ${''}
          ## What's New?
        `,
      }),
      new ExampleBuilder({
        description: 'A numbered TOC with a title when `List style = number`, `Ordered list style = increment`, and `Title = **Contents**`',
        before: dedent`
          <!-- toc -->
          ${''}
          ## Chapter One
          ### Section A
          ## Chapter Two
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          **Contents**
          ${''}
          1. [Chapter One](#chapter-one)
            2. [Section A](#section-a)
          3. [Chapter Two](#chapter-two)
          ${''}
          <!-- /toc -->
          ${''}
          ## Chapter One
          ### Section A
          ## Chapter Two
        `,
        options: {
          listStyle: 'number',
          orderedListStyle: 'increment',
          title: '**Contents**',
        },
      }),
      new ExampleBuilder({
        description: 'Explicit IDs are used as anchors and formatting is removed from TOC items when `Use explicit IDs = true` and `Strip formatting in TOC = true`',
        before: dedent`
          <!-- toc -->
          <!-- /toc -->
          ${''}
          ## **Bold** and _italic_ {#custom-id}
          ## [[Linked Note|Alias]] with \`code\`
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          - [Bold and italic](#custom-id)
          - [Alias with code](#alias-with-code)
          ${''}
          <!-- /toc -->
          ${''}
          ## **Bold** and _italic_ {#custom-id}
          ## [[Linked Note|Alias]] with \`code\`
        `,
        options: {
          useExplicitIds: true,
          stripFormattingInToc: true,
        },
      }),
      new ExampleBuilder({
        description: 'Headings can be excluded by literal text or `/regex/` and the heading range can be limited when `Exclude headings = Changelog\\n/^appendix/`, `Min level = 1`, and `Max level = 2`',
        before: dedent`
          # Guide
          ${''}
          <!-- TOC -->
          ${''}
          ## Usage
          ### Details
          ## Changelog
          ## Appendix A
        `,
        after: dedent`
          # Guide
          ${''}
          <!-- TOC -->
          ${''}
          - [Guide](#guide)
            - [Usage](#usage)
          ${''}
          <!-- /toc -->
          ${''}
          ## Usage
          ### Details
          ## Changelog
          ## Appendix A
        `,
        options: {
          excludeHeadings: ['Changelog', '/^appendix/'],
          minLevel: 1,
          maxLevel: 2,
        },
      }),
      new ExampleBuilder({
        description: 'Files without a `<!-- toc -->` marker are left unchanged',
        before: dedent`
          ## Heading 1
          ## Heading 2
        `,
        after: dedent`
          ## Heading 1
          ## Heading 2
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
            description: 'Uses an unordered list for the TOC',
          },
          {
            value: 'number',
            description: 'Uses an ordered list for the TOC',
          },
        ],
      }),
      new DropdownOptionBuilder<AutoTocOptions, TocBulletMarker>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.bullet-marker.name',
        descriptionKey: 'rules.auto-toc.bullet-marker.description',
        optionsKey: 'bulletMarker',
        records: [
          {
            value: '-',
            description: 'Uses `-` as the list item indicator',
          },
          {
            value: '*',
            description: 'Uses `*` as the list item indicator',
          },
          {
            value: '+',
            description: 'Uses `+` as the list item indicator',
          },
        ],
      }),
      new DropdownOptionBuilder<AutoTocOptions, TocOrderedListStyle>({
        OptionsClass: AutoTocOptions,
        nameKey: 'rules.auto-toc.ordered-list-style.name',
        descriptionKey: 'rules.auto-toc.ordered-list-style.description',
        optionsKey: 'orderedListStyle',
        records: [
          {
            value: 'always-one',
            description: 'Every list item uses `1.`',
          },
          {
            value: 'increment',
            description: 'List items are numbered incrementally across all items (i.e. `1.`, `2.`, `3.`, etc.)',
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

function getIgnoredRanges(text: string): TextRange[] {
  const ranges: TextRange[] = [];
  const yamlMatch = text.match(yamlRegex);
  if (yamlMatch) {
    ranges.push({start: yamlMatch.index, end: yamlMatch.index + yamlMatch[0].length});
  }

  for (const type of [MDAstTypes.Code, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      ranges.push({start: position.start.offset, end: position.end.offset});
    }
  }

  return ranges;
}

function isInRange(index: number, range: TextRange): boolean {
  return index >= range.start && index < range.end;
}

function findFirstMarker(text: string, markerRegex: RegExp, fromIndex: number, ignoredRanges: TextRange[]): RegExpMatchArray | null {
  for (const match of text.matchAll(markerRegex)) {
    if (match.index >= fromIndex && !ignoredRanges.some((range) => isInRange(match.index, range))) {
      return match;
    }
  }

  return null;
}

function getTocEntries(text: string, tocRegion: TextRange, ignoredRanges: TextRange[], options: AutoTocOptions): TocEntry[] {
  const minLevel = toInteger(options.minLevel, 2);
  const maxLevel = toInteger(options.maxLevel, 6);
  const excludeMatchers = getExcludeMatchers(options.excludeHeadings);
  const anchorOccurrences = new Map<string, number>();
  const entries: TocEntry[] = [];

  for (const match of text.matchAll(atxHeadingRegex)) {
    if (isInRange(match.index, tocRegion) || ignoredRanges.some((range) => isInRange(match.index, range))) {
      continue;
    }

    const level = match[1].length;
    if (level < minLevel || level > maxLevel) {
      continue;
    }

    let headingText = stripClosingSequence(match[2] ?? '');
    let explicitId: string | null = null;
    if (options.useExplicitIds) {
      const explicitIdMatch = headingText.match(explicitIdRegex);
      if (explicitIdMatch) {
        explicitId = explicitIdMatch[1];
        headingText = stripClosingSequence(headingText.substring(0, explicitIdMatch.index));
      }
    }

    const plainText = normalizeWhitespace(stripFormatting(resolveLinks(removeImageEmbeds(headingText))));
    const label = options.stripFormattingInToc ? plainText : normalizeWhitespace(resolveLinks(removeImageEmbeds(headingText)));
    if (label === '' || excludeMatchers.some((matcher) => matcher(headingText) || matcher(plainText))) {
      continue;
    }

    entries.push({
      level,
      label,
      anchor: getUniqueAnchor(explicitId ?? slugify(plainText), anchorOccurrences),
    });
  }

  return entries;
}

function buildTocLines(entries: TocEntry[], options: AutoTocOptions): string[] {
  if (entries.length === 0) {
    return [];
  }

  const indentSize = Math.max(0, toInteger(options.indentSize, 2));
  // indent relative to the shallowest included heading so the list never starts indented (which could turn it into a code block)
  const baseLevel = Math.min(...entries.map((entry) => entry.level));
  let itemNumber = 0;

  return entries.map((entry) => {
    let listIndicator: string;
    if (options.listStyle === 'number') {
      itemNumber++;
      listIndicator = options.orderedListStyle === 'increment' ? `${itemNumber}.` : '1.';
    } else {
      listIndicator = options.bulletMarker || '-';
    }

    return `${' '.repeat((entry.level - baseLevel) * indentSize)}${listIndicator} [${entry.label}](#${entry.anchor})`;
  });
}

function ensureBlankLineBeforeFollowingContent(text: string): string {
  if (text.trim() === '') {
    return text;
  }

  text = text.replace(/^[ \t]+/, '');
  if (/^\n[ \t]*\n/.test(text)) {
    return text;
  }

  return (text.startsWith('\n') ? '\n' : '\n\n') + text;
}

function stripClosingSequence(headingText: string): string {
  return headingText.trim().replace(closingHeadingSequenceRegex, '').trim();
}

function removeImageEmbeds(text: string): string {
  return text.replace(wikiImageEmbedRegex, '').replace(markdownImageRegex, '');
}

function resolveLinks(text: string): string {
  text = text.replace(wikiLinkRegex, (link: string, embedIndicator: string, target: string, _aliasGroup: string, alias: string) => {
    if (embedIndicator) {
      return link;
    }

    return alias ?? target;
  });

  return replaceUntilUnchanged(text, [markdownLinkRegex]);
}

function stripFormatting(text: string): string {
  let result = '';
  let lastIndex = 0;
  for (const match of text.matchAll(codeSpanRegex)) {
    const codeSpanStart = match.index + match[1].length;
    result += stripEmphasisAndHtml(text.substring(lastIndex, codeSpanStart)) + match[3].replace(/^ (.*[^ ].*) $/, '$1');
    lastIndex = match.index + match[0].length;
  }

  return result + stripEmphasisAndHtml(text.substring(lastIndex));
}

function stripEmphasisAndHtml(text: string): string {
  return replaceUntilUnchanged(text.replace(htmlTagRegex, ''), emphasisRegexes);
}

// the regexes capture the preceding character as the first group and the content to keep as the second group
function replaceUntilUnchanged(text: string, regexes: RegExp[]): string {
  let previousText: string;
  do {
    previousText = text;
    for (const regex of regexes) {
      text = text.replace(regex, '$1$2');
    }
  } while (text !== previousText);

  return text;
}

function normalizeWhitespace(text: string): string {
  return text.replace(/[ \t]+/g, ' ').trim();
}

function slugify(text: string): string {
  return unescapeMarkdownSpecialCharacters(text)
      .toLowerCase()
      .replace(/\s/g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-{2,}/g, '-')
      .replace(/^-+|-+$/g, '');
}

function getUniqueAnchor(baseAnchor: string, anchorOccurrences: Map<string, number>): string {
  let anchor = baseAnchor;
  while (anchorOccurrences.has(anchor)) {
    const occurrences = anchorOccurrences.get(baseAnchor) + 1;
    anchorOccurrences.set(baseAnchor, occurrences);
    anchor = `${baseAnchor}-${occurrences}`;
  }

  anchorOccurrences.set(anchor, 0);

  return anchor;
}

function getExcludeMatchers(excludeHeadings: string[] | string): ((headingText: string) => boolean)[] {
  const values = typeof excludeHeadings === 'string' ? excludeHeadings.split('\n') : excludeHeadings ?? [];
  const matchers: ((headingText: string) => boolean)[] = [];
  for (const value of values) {
    const trimmedValue = value.trim();
    if (trimmedValue === '') {
      continue;
    }

    if (trimmedValue.length > 2 && trimmedValue.startsWith('/') && trimmedValue.endsWith('/')) {
      try {
        const regex = new RegExp(trimmedValue.substring(1, trimmedValue.length - 1), 'i');
        matchers.push((headingText: string) => regex.test(headingText));
        continue;
      } catch {
        // invalid regexes are treated as literals
      }
    }

    const literal = trimmedValue.toLowerCase();
    matchers.push((headingText: string) => headingText.trim().toLowerCase() === literal);
  }

  return matchers;
}

function toInteger(value: unknown, defaultValue: number): number {
  if (value === null || value === undefined || (typeof value === 'string' && value.trim() === '')) {
    return defaultValue;
  }

  const parsedValue = Number(value);

  return Number.isFinite(parsedValue) ? Math.floor(parsedValue) : defaultValue;
}
