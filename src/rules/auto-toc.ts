import {IgnoreTypes} from '../utils/ignore-types';
import {MDAstTypes, getPositions} from '../utils/mdast';
import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import {unescapeMarkdownSpecialCharacters} from '../utils/strings';
import dedent from 'ts-dedent';
import type {Position} from 'unist';

type ListStyle = 'bullet' | 'number';
type OrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle: ListStyle = 'bullet';
  bulletMarker: string = '-';
  orderedListStyle: OrderedListStyle = 'always-one';
  indentSize: number = 2;
  minLevel: number = 2;
  maxLevel: number = 6;
  title: string = '';
  useExplicitIds: boolean = false;
  stripFormattingInToc: boolean = false;
  excludeHeadings: string[] = [];
}

type HeadingInfo = {
  level: number,
  text: string,
  plainText: string,
  explicitId: string | null,
};

const tocStartMarker = /<!--\s*toc\s*-->/i;
const tocEndMarker = /<!--\s*\/\s*toc\s*-->/i;

function removeImageEmbeds(text: string): string {
  return text
      .replace(/!\[\[[^\]\r\n]*\]\]/g, '')
      .replace(/!\[[^\]\r\n]*\]\((?:\\.|[^()\\\r\n]|\([^()\\\r\n]*\))*\)/g, '');
}

function resolveLinks(text: string): string {
  let resolvedText = text;
  let previousText = '';

  while (resolvedText !== previousText) {
    previousText = resolvedText;
    resolvedText = resolvedText
        .replace(/\[\[([^\]\r\n]+)\]\]/g, (_match: string, linkText: string) => {
          const displayText = linkText.split('|');
          return displayText[displayText.length - 1];
        })
        .replace(/\[([^\]\r\n]*)\]\((?:\\.|[^()\\\r\n]|\([^()\\\r\n]*\))*\)/g, (_match: string, linkText: string) => linkText);
  }

  return resolvedText;
}

function stripFormatting(text: string): string {
  return unescapeMarkdownSpecialCharacters(text)
      .replace(/<[^>\r\n]*>/g, '')
      .replace(/`([^`\r\n]*)`/g, '$1')
      .replace(/(\*\*|__|~~|==)(.*?)\1/g, '$2')
      .replace(/([*_])([^*_\r\n]+)\1/g, '$2')
      .replace(/[*~=`]/g, '');
}

function getPlainText(text: string): string {
  return stripFormatting(resolveLinks(removeImageEmbeds(text)));
}

function getHeadingInfo(text: string, position: Position): HeadingInfo | null {
  const rawHeading = text.substring(position.start.offset, position.end.offset).replace(/\r$/, '');
  const headingMatch = rawHeading.match(/^ {0,3}(#{1,6})(?:[ \t]+(.*?)|[ \t]*)$/);
  if (!headingMatch) {
    return null;
  }

  const level = headingMatch[1].length;
  let headingText = (headingMatch[2] ?? '').trim();
  headingText = headingText.replace(/[ \t]+#+[ \t]*$/, '').trim();

  const explicitIdMatch = headingText.match(/\s+\{#([^}\s]+)\}\s*$/);
  const explicitId = explicitIdMatch ? explicitIdMatch[1] : null;
  if (explicitIdMatch) {
    headingText = headingText.substring(0, explicitIdMatch.index).trim();
  }

  return {
    level,
    text: headingText,
    plainText: getPlainText(headingText),
    explicitId,
  };
}

function matchesExcludedHeading(headingText: string, excludedHeadings: string[]): boolean {
  for (const configuredHeading of excludedHeadings) {
    const pattern = configuredHeading.trim();
    if (pattern.length === 0) {
      continue;
    }

    if (pattern.startsWith('/') && pattern.endsWith('/') && pattern.length > 1) {
      try {
        if (new RegExp(pattern.substring(1, pattern.length - 1), 'i').test(headingText)) {
          return true;
        }
      } catch {
        continue;
      }
    } else if (pattern.toLowerCase() === headingText.toLowerCase()) {
      return true;
    }
  }

  return false;
}

function getExcludedHeadings(options: AutoTocOptions): string[] {
  if (!Array.isArray(options.excludeHeadings)) {
    return [];
  }

  return options.excludeHeadings.filter((heading): heading is string => typeof heading === 'string');
}

function getHeadings(text: string, tocStart: number, tocEnd: number, options: AutoTocOptions): HeadingInfo[] {
  const minLevel = options.minLevel ?? 2;
  const maxLevel = options.maxLevel ?? 6;
  const excludedHeadings = getExcludedHeadings(options);
  const headings: HeadingInfo[] = [];

  const positions = getPositions(MDAstTypes.Heading, text);
  positions.sort((a, b) => a.start.offset - b.start.offset);

  for (const position of positions) {
    if (position.start.offset < tocEnd && position.end.offset > tocStart) {
      continue;
    }

    const heading = getHeadingInfo(text, position);
    if (heading === null || heading.level < minLevel || heading.level > maxLevel || matchesExcludedHeading(heading.plainText, excludedHeadings)) {
      continue;
    }

    headings.push(heading);
  }

  return headings;
}

function createAnchor(heading: HeadingInfo, useExplicitIds: boolean, usedAnchors: Map<string, number>): string {
  const baseAnchor = useExplicitIds && heading.explicitId !== null ?
    heading.explicitId :
    heading.plainText
        .toLowerCase()
        .replace(/\s+/g, '-')
        .replace(/[^a-z0-9-_]/g, '')
        .replace(/-+/g, '-')
        .replace(/^-+|-+$/g, '');
  const duplicateNumber = usedAnchors.get(baseAnchor) ?? 0;
  usedAnchors.set(baseAnchor, duplicateNumber + 1);

  return duplicateNumber === 0 ? baseAnchor : `${baseAnchor}-${duplicateNumber}`;
}

function createTocItems(headings: HeadingInfo[], options: AutoTocOptions): string[] {
  const indentSize = Math.max(0, options.indentSize ?? 2);
  const minLevel = options.minLevel ?? 2;
  const useNumberedList = options.listStyle === 'number';
  const bulletMarker = options.bulletMarker || '-';
  const usedAnchors = new Map<string, number>();
  const items: string[] = [];

  headings.forEach((heading, index) => {
    const indent = ' '.repeat(Math.max(0, heading.level - minLevel) * indentSize);
    const listMarker = useNumberedList ?
      `${options.orderedListStyle === 'increment' ? index + 1 : 1}.` :
      bulletMarker;
    const tocText = options.stripFormattingInToc ?
      heading.plainText :
      resolveLinks(removeImageEmbeds(heading.text));
    const anchor = createAnchor(heading, options.useExplicitIds ?? false, usedAnchors);
    items.push(`${indent}${listMarker} [${tocText}](#${anchor})`);
  });

  return items;
}

function removeLeadingBlankLines(text: string): string {
  return text.replace(/^(?:[ \t]*(?:\r\n|\n))+/, '');
}

function replaceTocRegion(text: string, startMatch: RegExpExecArray, endMatch: RegExpExecArray | null, options: AutoTocOptions): string {
  const newline = text.includes('\r\n') ? '\r\n' : '\n';
  const tocStart = startMatch.index + startMatch[0].length;
  const relativeEnd = endMatch?.index ?? text.length - tocStart;
  const tocEnd = endMatch ? tocStart + relativeEnd : text.length;
  const endMarker = endMatch?.[0] ?? '<!-- /toc -->';
  const endMarkerEnd = endMatch ? tocEnd + endMarker.length : text.length;
  const headings = getHeadings(text, tocStart, tocEnd, options);
  const tocItems = createTocItems(headings, options);
  const title = options.title?.trim() ?? '';
  let tocContent = '';

  if (title.length > 0) {
    tocContent += title + newline + newline;
  }

  if (tocItems.length > 0) {
    tocContent += tocItems.join(newline) + newline + newline;
  }

  const contentAfterEnd = removeLeadingBlankLines(text.substring(endMarkerEnd));
  return text.substring(0, tocStart) +
    newline + newline +
    tocContent +
    endMarker +
    newline + newline +
    contentAfterEnd;
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
    const startMatch = tocStartMarker.exec(text);
    if (!startMatch) {
      return text;
    }

    const tocStart = startMatch.index + startMatch[0].length;
    const endMatch = tocEndMarker.exec(text.substring(tocStart));
    return replaceTocRegion(text, startMatch, endMatch, options);
  }

  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Generates a table of contents between the `toc` markers.',
        before: dedent`
          <!-- toc -->
          <!-- /toc -->

          ## Introduction
          ### Details
        `,
        after: dedent`
          <!-- toc -->

          - [Introduction](#introduction)
            - [Details](#details)

          <!-- /toc -->

          ## Introduction
          ### Details
        `,
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
            description: 'Uses a bullet list.',
          },
          {
            value: 'number',
            description: 'Uses a numbered list.',
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
            description: 'Uses `1.` for every item.',
          },
          {
            value: 'increment',
            description: 'Increments numbering across all items.',
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
