import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';
import {getPositions, MDAstTypes} from '../utils/mdast';
import {yamlRegex} from '../utils/regex';

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

type Range = {start: number, end: number};

type TocHeading = {
  level: number,
  rawText: string,
  plainText: string,
  explicitId: string | null,
};

const tocStartRegex = /<!--\s*toc\s*-->/gi;
const tocEndRegex = /<!--\s*\/toc\s*-->/gi;
const atxHeadingRegex = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/gm;
const closingHashesRegex = /(?:^|[ \t]+)#+[ \t]*$/;
const explicitIdRegex = /[ \t]*\{#([^\s{}]+)\}$/;
const defaultEndMarker = '<!-- /toc -->';

function getIgnoredRanges(text: string): Range[] {
  const ranges: Range[] = [];
  for (const type of [MDAstTypes.Code, MDAstTypes.Math]) {
    for (const position of getPositions(type, text)) {
      ranges.push({start: position.start.offset, end: position.end.offset});
    }
  }

  const yaml = text.match(yamlRegex);
  if (yaml) {
    ranges.push({start: yaml.index, end: yaml.index + yaml[0].length});
  }

  return ranges;
}

function isIgnored(index: number, ranges: Range[]): boolean {
  return ranges.some((range) => index >= range.start && index < range.end);
}

function findFirstMatch(text: string, regex: RegExp, fromIndex: number, ignoredRanges: Range[]): RegExpExecArray | null {
  regex.lastIndex = fromIndex;
  let match: RegExpExecArray | null;
  while ((match = regex.exec(text)) !== null) {
    if (!isIgnored(match.index, ignoredRanges)) {
      return match;
    }
  }

  return null;
}

function toPlainText(text: string): string {
  return text
      .replace(/!\[\[[^\]]*\]\]/g, '')
      .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
      .replace(/\[\[([^\]|]*)\|([^\]]*)\]\]/g, '$2')
      .replace(/\[\[([^\]]*)\]\]/g, '$1')
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/\[([^\]]*)\]\[[^\]]*\]/g, '$1')
      .replace(/<\/?[a-zA-Z][^>]*>/g, '')
      .replace(/`+([^`]*?)`+/g, '$1')
      .replace(/\*\*(.+?)\*\*/g, '$1')
      .replace(/(^|\W)__(.+?)__(?!\w)/g, '$1$2')
      .replace(/\*(.+?)\*/g, '$1')
      .replace(/(^|\W)_(.+?)_(?!\w)/g, '$1$2')
      .replace(/~~(.+?)~~/g, '$1')
      .replace(/==(.+?)==/g, '$1')
      .replace(/\s+/g, ' ')
      .trim();
}

function toAnchor(plainText: string): string {
  return plainText
      .toLowerCase()
      .replace(/\s/g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-+|-+$/g, '');
}

function getHeadings(text: string, offset: number, ignoredRanges: Range[], useExplicitIds: boolean): TocHeading[] {
  const headings: TocHeading[] = [];
  for (const match of text.matchAll(atxHeadingRegex)) {
    if (isIgnored(offset + match.index, ignoredRanges)) {
      continue;
    }

    let rawText = (match[2] ?? '').replace(closingHashesRegex, '').trim();
    let explicitId: string | null = null;
    if (useExplicitIds) {
      const idMatch = rawText.match(explicitIdRegex);
      if (idMatch) {
        explicitId = idMatch[1];
        rawText = rawText.substring(0, idMatch.index).trim();
      }
    }

    if (rawText === '' && explicitId === null) {
      continue;
    }

    headings.push({
      level: match[1].length,
      rawText,
      plainText: toPlainText(rawText),
      explicitId,
    });
  }

  return headings;
}

function buildExcludeMatchers(excludeHeadings: string[] | string): ((value: string) => boolean)[] {
  const entries = typeof excludeHeadings === 'string' ? excludeHeadings.split('\n') : (excludeHeadings ?? []);
  const matchers: ((value: string) => boolean)[] = [];
  for (const entry of entries) {
    const trimmed = entry.trim();
    if (trimmed === '') {
      continue;
    }

    const regexParts = trimmed.match(/^\/(.+)\/([a-z]*)$/);
    if (regexParts) {
      try {
        const flags = regexParts[2].replace(/[gyi]/g, '') + 'i';
        const regex = new RegExp(regexParts[1], flags);
        matchers.push((value: string) => regex.test(value));
      } catch {
        // invalid regexes are ignored
      }

      continue;
    }

    const literal = trimmed.toLowerCase();
    matchers.push((value: string) => value.trim().toLowerCase() === literal);
  }

  return matchers;
}

function toInteger(value: unknown, defaultValue: number): number {
  const parsed = parseInt(String(value), 10);
  return isNaN(parsed) ? defaultValue : parsed;
}

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
    const ignoredRanges = getIgnoredRanges(text);

    const startMatch = findFirstMatch(text, tocStartRegex, 0, ignoredRanges);
    if (!startMatch) {
      return text;
    }

    const startMarkerEnd = startMatch.index + startMatch[0].length;
    const endMatch = findFirstMatch(text, tocEndRegex, startMarkerEnd, ignoredRanges);
    const endMarker = endMatch ? endMatch[0] : defaultEndMarker;
    const regionEnd = endMatch ? endMatch.index + endMatch[0].length : startMarkerEnd;

    const textBefore = text.substring(0, startMatch.index);
    const textAfter = text.substring(regionEnd);

    const minLevel = Math.max(1, toInteger(options.minLevel, 2));
    const maxLevel = Math.min(6, toInteger(options.maxLevel, 6));
    const indentSize = Math.max(0, toInteger(options.indentSize, 2));
    const excludeMatchers = buildExcludeMatchers(options.excludeHeadings);

    const headings = [
      ...getHeadings(textBefore, 0, ignoredRanges, options.useExplicitIds),
      ...getHeadings(textAfter, regionEnd, ignoredRanges, options.useExplicitIds),
    ].filter((heading) => heading.level >= minLevel && heading.level <= maxLevel &&
      !excludeMatchers.some((matches) => matches(heading.rawText) || matches(heading.plainText)));

    const usedAnchors = new Set<string>();
    const anchorCounts = new Map<string, number>();
    const getUniqueAnchor = (base: string): string => {
      let anchor = base;
      if (usedAnchors.has(anchor)) {
        let count = anchorCounts.get(base) ?? 0;
        do {
          count++;
          anchor = `${base}-${count}`;
        } while (usedAnchors.has(anchor));

        anchorCounts.set(base, count);
      }

      usedAnchors.add(anchor);
      return anchor;
    };

    const topLevel = Math.min(...headings.map((heading) => heading.level));
    const tocLines = headings.map((heading, index) => {
      const anchor = getUniqueAnchor(heading.explicitId ?? toAnchor(heading.plainText));
      const displayText = options.stripFormattingInToc ? heading.plainText : heading.rawText;
      const indent = ' '.repeat((heading.level - topLevel) * indentSize);

      let listIndicator: string;
      if (options.listStyle === 'number') {
        listIndicator = options.orderedListStyle === 'increment' ? `${index + 1}.` : '1.';
      } else {
        listIndicator = options.bulletMarker;
      }

      return `${indent}${listIndicator} [${displayText}](#${anchor})`;
    });

    const sections: string[] = [];
    const title = (options.title ?? '').trim();
    if (title !== '') {
      sections.push(title);
    }

    if (tocLines.length > 0) {
      sections.push(tocLines.join('\n'));
    }

    let region = startMatch[0] + '\n\n';
    if (sections.length > 0) {
      region += sections.join('\n\n') + '\n\n';
    }
    region += endMarker;

    let remainder = textAfter;
    if (remainder.trim() !== '') {
      const withoutBlankLines = remainder.replace(/^(?:[ \t]*\r?\n)+/, '');
      remainder = withoutBlankLines === remainder ? remainder.trimStart() : withoutBlankLines;
      remainder = '\n\n' + remainder;
    }

    return textBefore + region + remainder;
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Generates a table of contents where the `<!-- toc -->` marker is and inserts the missing end marker',
        before: dedent`
          # Document Title
          ${''}
          <!-- toc -->
          ${''}
          ## Introduction
          ${''}
          ### Getting **Started**
          ${''}
          ## Usage
        `,
        after: dedent`
          # Document Title
          ${''}
          <!-- toc -->
          ${''}
          - [Introduction](#introduction)
            - [Getting **Started**](#getting-started)
          - [Usage](#usage)
          ${''}
          <!-- /toc -->
          ${''}
          ## Introduction
          ${''}
          ### Getting **Started**
          ${''}
          ## Usage
        `,
      }),
      new ExampleBuilder({
        description: 'Updates an existing table of contents, deduplicating anchors and ignoring headings in code blocks',
        before: dedent`
          <!-- TOC -->
          - [Old entry](#old-entry)
          <!-- /TOC -->
          ## Notes
          ${''}
          \`\`\`md
          ## Not a heading
          \`\`\`
          ${''}
          ## Notes
        `,
        after: dedent`
          <!-- TOC -->
          ${''}
          - [Notes](#notes)
          - [Notes](#notes-1)
          ${''}
          <!-- /TOC -->
          ${''}
          ## Notes
          ${''}
          \`\`\`md
          ## Not a heading
          \`\`\`
          ${''}
          ## Notes
        `,
      }),
      new ExampleBuilder({
        description: 'Uses a title, numbered list items that increment, stripped formatting, and explicit ids when `title = \'**Contents**\'`, `listStyle = \'number\'`, `orderedListStyle = \'increment\'`, `stripFormattingInToc = true`, and `useExplicitIds = true`',
        before: dedent`
          <!-- toc -->
          <!-- /toc -->
          ${''}
          ## The [[Setup Guide|setup]] {#install}
          ${''}
          ### Using \`config\` files
          ${''}
          ## Changelog
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          **Contents**
          ${''}
          1. [The setup](#install)
             2. [Using config files](#using-config-files)
          3. [Changelog](#changelog)
          ${''}
          <!-- /toc -->
          ${''}
          ## The [[Setup Guide|setup]] {#install}
          ${''}
          ### Using \`config\` files
          ${''}
          ## Changelog
        `,
        options: {
          title: '**Contents**',
          listStyle: 'number',
          orderedListStyle: 'increment',
          indentSize: 3,
          stripFormattingInToc: true,
          useExplicitIds: true,
        },
      }),
      new ExampleBuilder({
        description: 'Excludes headings that match `excludeHeadings` when `excludeHeadings = [\'changelog\', \'/^appendix/\']`',
        before: dedent`
          <!-- toc -->
          ${''}
          ## Overview
          ## Changelog
          ## Appendix A
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          - [Overview](#overview)
          ${''}
          <!-- /toc -->
          ${''}
          ## Overview
          ## Changelog
          ## Appendix A
        `,
        options: {
          excludeHeadings: ['changelog', '/^appendix/'],
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
            description: 'Uses a bulleted list for the table of contents',
          },
          {
            value: 'number',
            description: 'Uses a numbered list for the table of contents',
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
            description: 'Uses `1.` for every numbered list item',
          },
          {
            value: 'increment',
            description: 'Increments the number for every list item in the table of contents',
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
