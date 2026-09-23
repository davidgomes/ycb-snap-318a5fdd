import {IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';

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

type TocHeading = {
  level: number,
  text: string,
  explicitId: string | null,
};

const tocStartRegex = /<!--\s*toc\s*-->/i;
const tocEndRegex = /<!--\s*\/toc\s*-->/i;
const atxHeadingRegex = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/;
const explicitIdRegex = /[ \t]*\{#([^}\s]+)\}[ \t]*$/;
const defaultEndMarker = '<!-- /toc -->';

function stripClosingHashes(text: string): string {
  if (/^#+$/.test(text)) {
    return '';
  }

  return text.replace(/[ \t]+#+[ \t]*$/, '').trim();
}

function stripFormatting(text: string): string {
  return text
      .replace(/!\[\[[^\]]*\]\]/g, '')
      .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
      .replace(/\[\[([^\]|]+)(?:\|([^\]]*))?\]\]/g, (_, target: string, alias?: string) => alias ?? target)
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/`([^`]*)`/g, '$1')
      .replace(/(\*\*|__)(.+?)\1/g, '$2')
      .replace(/(^|[^\w*])\*(?!\s)(.+?)\*(?![\w*])/g, '$1$2')
      .replace(/(^|[^\w])_(?!\s)(.+?)_(?!\w)/g, '$1$2')
      .replace(/~~(.+?)~~/g, '$1')
      .replace(/==(.+?)==/g, '$1')
      .replace(/<[^>]+>/g, '')
      .replace(/\s+/g, ' ')
      .trim();
}

function slugify(text: string): string {
  return stripClosingHashes(stripFormatting(text))
      .toLowerCase()
      .replace(/\s/g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-+|-+$/g, '');
}

function parseHeadings(text: string, useExplicitIds: boolean): TocHeading[] {
  const headings: TocHeading[] = [];
  for (const line of text.split('\n')) {
    const match = line.match(atxHeadingRegex);
    if (!match) {
      continue;
    }

    let headingText = stripClosingHashes((match[2] ?? '').trim());
    let explicitId: string | null = null;
    if (useExplicitIds) {
      const idMatch = headingText.match(explicitIdRegex);
      if (idMatch) {
        explicitId = idMatch[1];
        headingText = headingText.substring(0, idMatch.index).trim();
      }
    }

    if (headingText === '' && explicitId === null) {
      continue;
    }

    headings.push({level: match[1].length, text: headingText, explicitId});
  }

  return headings;
}

function buildExcludeMatchers(excludeHeadings: string[]): ((heading: TocHeading) => boolean)[] {
  const matchers: ((heading: TocHeading) => boolean)[] = [];
  for (const rawEntry of excludeHeadings ?? []) {
    const entry = rawEntry.trim();
    if (entry === '') {
      continue;
    }

    const regexMatch = entry.match(/^\/(.+)\/([a-z]*)$/);
    if (regexMatch) {
      let regex: RegExp;
      try {
        const flags = regexMatch[2].replace(/[gi]/g, '') + 'i';
        regex = new RegExp(regexMatch[1], flags);
      } catch {
        continue;
      }

      matchers.push((heading) => regex.test(heading.text) || regex.test(stripFormatting(heading.text)));
      continue;
    }

    const literal = entry.toLowerCase();
    matchers.push((heading) => heading.text.toLowerCase() === literal || stripFormatting(heading.text).toLowerCase() === literal);
  }

  return matchers;
}

function toNumber(value: unknown, fallback: number): number {
  const num = Number(value);
  return Number.isFinite(num) ? num : fallback;
}

function buildTocList(headings: TocHeading[], options: AutoTocOptions): string {
  const minLevel = toNumber(options.minLevel, 2);
  const maxLevel = toNumber(options.maxLevel, 6);
  const indentSize = Math.max(0, toNumber(options.indentSize, 2));
  const excludeMatchers = buildExcludeMatchers(options.excludeHeadings);

  const included = headings.filter((heading) =>
    heading.level >= minLevel &&
    heading.level <= maxLevel &&
    !excludeMatchers.some((matcher) => matcher(heading)),
  );
  if (included.length === 0) {
    return '';
  }

  const baseLevel = Math.min(...included.map((heading) => heading.level));
  const usedAnchors = new Set<string>();
  const anchorCounts = new Map<string, number>();
  const lines: string[] = [];
  let itemNumber = 0;
  for (const heading of included) {
    const baseAnchor = heading.explicitId ?? slugify(heading.text);
    let count = anchorCounts.get(baseAnchor) ?? 0;
    let anchor = count === 0 ? baseAnchor : `${baseAnchor}-${count}`;
    while (usedAnchors.has(anchor)) {
      count++;
      anchor = `${baseAnchor}-${count}`;
    }
    anchorCounts.set(baseAnchor, count + 1);
    usedAnchors.add(anchor);

    itemNumber++;
    let marker: string;
    if (options.listStyle === 'number') {
      marker = options.orderedListStyle === 'increment' ? `${itemNumber}.` : '1.';
    } else {
      marker = options.bulletMarker || '-';
    }

    const displayText = options.stripFormattingInToc ? stripFormatting(heading.text) : heading.text;
    const indent = ' '.repeat((heading.level - baseLevel) * indentSize);
    lines.push(`${indent}${marker} [${displayText}](#${anchor})`);
  }

  return lines.join('\n');
}

@RuleBuilder.register
export default class AutoToc extends RuleBuilder<AutoTocOptions> {
  constructor() {
    super({
      nameKey: 'rules.auto-toc.name',
      descriptionKey: 'rules.auto-toc.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [IgnoreTypes.code, IgnoreTypes.math, IgnoreTypes.yaml],
    });
  }
  get OptionsClass(): new () => AutoTocOptions {
    return AutoTocOptions;
  }
  apply(text: string, options: AutoTocOptions): string {
    const startMatch = tocStartRegex.exec(text);
    if (!startMatch) {
      return text;
    }

    const startMarkerEnd = startMatch.index + startMatch[0].length;
    const before = text.substring(0, startMarkerEnd);
    let afterStart = text.substring(startMarkerEnd);

    let endMarker = defaultEndMarker;
    const endMatch = tocEndRegex.exec(afterStart);
    if (endMatch) {
      endMarker = endMatch[0];
      afterStart = afterStart.substring(endMatch.index + endMatch[0].length);
    }

    const rest = afterStart.replace(/^[ \t]*\n+/, '');
    const headings = parseHeadings(before + '\n' + rest, options.useExplicitIds);
    const tocList = buildTocList(headings, options);

    const title = (options.title ?? '').trim();
    const body = [title, tocList].filter((part) => part !== '').join('\n\n');
    let region = '\n\n' + (body !== '' ? body + '\n\n' : '') + endMarker;
    if (rest.trim() !== '') {
      region += '\n\n';
    } else if (afterStart.includes('\n')) {
      region += '\n';
    }

    return before + region + (rest.trim() !== '' ? rest : '');
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'A table of contents is generated after the `<!-- toc -->` marker and the missing end marker is added',
        before: dedent`
          # Title
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
          # Title
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
        description: 'An existing table of contents is updated with a title, numbered items, and deduplicated anchors',
        before: dedent`
          <!-- toc -->
          - [Old](#old)
          <!-- /toc -->
          ## Notes
          ## Notes
          ## [[Linked Page|Alias]]
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          **Contents**
          ${''}
          1. [Notes](#notes)
          2. [Notes](#notes-1)
          3. [Alias](#alias)
          ${''}
          <!-- /toc -->
          ${''}
          ## Notes
          ## Notes
          ## [[Linked Page|Alias]]
        `,
        options: {
          title: '**Contents**',
          listStyle: 'number',
          orderedListStyle: 'increment',
          stripFormattingInToc: true,
        },
      }),
      new ExampleBuilder({
        description: 'Files without a `<!-- toc -->` marker are left unchanged',
        before: dedent`
          ## Heading
        `,
        after: dedent`
          ## Heading
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
            description: 'Every ordered list item uses `1.`',
          },
          {
            value: 'increment',
            description: 'Ordered list items are numbered incrementally across the whole table of contents',
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
