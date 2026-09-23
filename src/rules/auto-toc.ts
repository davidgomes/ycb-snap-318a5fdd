import {IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';

type TocListStyle = 'bullet' | 'number';
type TocOrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle?: TocListStyle = 'bullet';
  bulletMarker?: string = '-';
  orderedListStyle?: TocOrderedListStyle = 'always-one';
  indentSize?: number = 2;
  minLevel?: number = 2;
  maxLevel?: number = 6;
  title?: string = '';
  useExplicitIds?: boolean = false;
  stripFormattingInToc?: boolean = false;
  excludeHeadings?: string[] = [];
}

const START_MARKER = /<!--\s*toc\s*-->/i;
const END_MARKER = /<!--\s*\/toc\s*-->/i;
const ATX_HEADING = /^(#{1,6})[ \t]+(.*)$/;
const EXPLICIT_ID = /\s+\{#([^}]*)\}\s*$/;
const TRAILING_HASHES = /[ \t]+#+\s*$/;

@RuleBuilder.register
export default class AutoToc extends RuleBuilder<AutoTocOptions> {
  constructor() {
    super({
      nameKey: 'rules.auto-toc.name',
      descriptionKey: 'rules.auto-toc.description',
      type: RuleType.HEADING,
      ruleIgnoreTypes: [IgnoreTypes.code, IgnoreTypes.math, IgnoreTypes.yaml],
    });
  }
  get OptionsClass(): new () => AutoTocOptions {
    return AutoTocOptions;
  }
  apply(text: string, options: AutoTocOptions): string {
    const start = START_MARKER.exec(text);
    if (!start || start.index === undefined) {
      return text;
    }

    const startLine = lineStartIndex(text, start.index);
    const indent = markerIndent(text, startLine, start.index);
    const regionStart = indent === null ? start.index : startLine;

    const endSearchFrom = start.index + start[0].length;
    const end = END_MARKER.exec(text.slice(endSearchFrom));
    let regionEnd: number;
    if (end && end.index !== undefined) {
      const endIndex = endSearchFrom + end.index;
      regionEnd = consumeMarkerLine(text, endIndex + end[0].length);
    } else {
      regionEnd = consumeMarkerLine(text, endSearchFrom);
    }

    const headings = collectHeadings(text, regionStart, regionEnd, options);
    const toc = renderToc(headings, options, indent ?? '');
    const prefix = text.slice(0, regionStart);
    let suffix = text.slice(regionEnd).replace(/^(?:\r?\n)+/, '');
    if (suffix.length > 0) {
      suffix = '\n' + suffix;
    }

    return prefix + toc + suffix;
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Inserts a table of contents for ATX headings when `<!-- toc -->` is present and adds a closing marker when it is missing',
        before: dedent`
          <!-- toc -->
          # Ignored
          ## Second
          ### Third
        `,
        after: dedent`
          <!-- toc -->

          - [Second](#second)
            - [Third](#third)

          <!-- /toc -->

          # Ignored
          ## Second
          ### Third
        `,
      }),
      new ExampleBuilder({
        description: 'Updates an existing table of contents, skips excluded headings, and uses explicit ids',
        before: dedent`
          <!-- TOC -->
          old
          <!-- /TOC -->
          ## Keep {#custom}
          ## Skip Me
          ## **Styled**
        `,
        after: dedent`
          <!-- toc -->

          Contents

          1. [Keep](#custom)
          2. [Styled](#styled)

          <!-- /toc -->

          ## Keep {#custom}
          ## Skip Me
          ## **Styled**
        `,
        options: {
          listStyle: 'number',
          orderedListStyle: 'increment',
          title: 'Contents',
          useExplicitIds: true,
          stripFormattingInToc: true,
          excludeHeadings: ['skip me'],
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
          {value: 'bullet', description: 'Bulleted list'},
          {value: 'number', description: 'Numbered list'},
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
          {value: 'always-one', description: 'Every item uses 1.'},
          {value: 'increment', description: 'Increment numbers across all items'},
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

type TocHeading = {
  level: number;
  label: string;
  anchor: string;
};

function collectHeadings(text: string, regionStart: number, regionEnd: number, options: AutoTocOptions): TocHeading[] {
  const minLevel = toInt(options.minLevel, 2);
  const maxLevel = toInt(options.maxLevel, 6);
  const exclude = compileExclusions(options.excludeHeadings ?? []);
  const used = new Set<string>();
  const headings: TocHeading[] = [];
  const lines = text.split(/\r?\n/);
  let offset = 0;

  for (const line of lines) {
    const lineEnd = offset + line.length;
    const insideToc = offset >= regionStart && offset < regionEnd;
    if (!insideToc) {
      const match = line.match(ATX_HEADING);
      if (match) {
        const level = match[1].length;
        if (level >= minLevel && level <= maxLevel) {
          const parsed = parseHeading(match[2], options.useExplicitIds === true);
          if (!isExcluded(parsed.matchText, exclude)) {
            headings.push({
              level,
              label: options.stripFormattingInToc ? parsed.plain : parsed.rawLabel,
              anchor: dedupeAnchor(parsed.anchor, used),
            });
          }
        }
      }
    }
    offset = lineEnd + 1;
  }

  return headings;
}

function parseHeading(raw: string, useExplicitIds: boolean): {rawLabel: string, plain: string, matchText: string, anchor: string} {
  let text = raw.replace(TRAILING_HASHES, '').trim();
  let explicit: string | null = null;
  const idMatch = text.match(EXPLICIT_ID);
  if (idMatch) {
    explicit = idMatch[1];
    text = text.slice(0, idMatch.index).trim();
  }
  const plain = stripFormatting(text).trim();
  const anchor = useExplicitIds && explicit !== null ? explicit.trim() : slugify(plain);
  return {rawLabel: text, plain, matchText: text, anchor};
}

function stripFormatting(text: string): string {
  let value = text;
  value = value.replace(/!\[\[[^\]]*\]\]/g, '');
  value = value.replace(/!\[[^\]]*\]\([^)]*\)/g, '');
  value = value.replace(/\[\[([^\]|\n]+)\|([^\]]+)\]\]/g, '$2');
  value = value.replace(/\[\[([^\]]+)\]\]/g, (_, target: string) => target);
  value = value.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1');
  value = value.replace(/`([^`]*)`/g, '$1');
  value = value.replace(/~~([^~]*)~~/g, '$1');
  value = value.replace(/==([^=]*)==/g, '$1');
  value = value.replace(/<\/?[^>\n]+>/g, '');
  let previous = '';
  while (previous !== value) {
    previous = value;
    value = value.replace(/(\*\*|__)([\s\S]*?)\1/g, '$2');
    value = value.replace(/(\*|_)([\s\S]*?)\1/g, '$2');
  }
  return value;
}

function slugify(text: string): string {
  return text
      .toLowerCase()
      .replace(/ /g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-+|-+$/g, '');
}

function dedupeAnchor(base: string, used: Set<string>): string {
  if (!used.has(base)) {
    used.add(base);
    return base;
  }
  let i = 1;
  while (used.has(`${base}-${i}`)) {
    i++;
  }
  const next = `${base}-${i}`;
  used.add(next);
  return next;
}

function compileExclusions(patterns: string[]): {literals: Set<string>, regexes: RegExp[]} {
  const literals = new Set<string>();
  const regexes: RegExp[] = [];
  for (const pattern of patterns) {
    const trimmed = pattern.trim();
    if (trimmed.length === 0) {
      continue;
    }
    if (trimmed.length > 2 && trimmed.startsWith('/') && trimmed.endsWith('/')) {
      try {
        regexes.push(new RegExp(trimmed.slice(1, -1), 'i'));
        continue;
      } catch {
        // Fall through and treat an invalid pattern as a literal.
      }
    }
    literals.add(trimmed.toLowerCase());
  }
  return {literals, regexes};
}

function isExcluded(heading: string, exclude: {literals: Set<string>, regexes: RegExp[]}): boolean {
  const key = heading.trim().toLowerCase();
  if (exclude.literals.has(key)) {
    return true;
  }
  return exclude.regexes.some((regex) => regex.test(heading));
}

function renderToc(headings: TocHeading[], options: AutoTocOptions, indent: string): string {
  const minLevel = toInt(options.minLevel, 2);
  const indentSize = Math.max(0, toInt(options.indentSize, 2));
  const title = options.title ?? '';
  const lines: string[] = [`${indent}<!-- toc -->`, ''];
  if (title !== '') {
    lines.push(title, '');
  }
  let number = 1;
  for (const heading of headings) {
    const levelIndent = ' '.repeat(Math.max(0, heading.level - minLevel) * indentSize);
    const marker = options.listStyle === 'number' ?
      `${options.orderedListStyle === 'increment' ? number++ : 1}.` :
      (options.bulletMarker || '-');
    lines.push(`${levelIndent}${marker} [${heading.label}](#${heading.anchor})`);
  }
  if (headings.length > 0) {
    lines.push('');
  }
  lines.push(`${indent}<!-- /toc -->`, '');
  return lines.join('\n');
}

function lineStartIndex(text: string, index: number): number {
  const newline = text.lastIndexOf('\n', index - 1);
  return newline === -1 ? 0 : newline + 1;
}

function markerIndent(text: string, lineStart: number, markerIndex: number): string | null {
  const prefix = text.slice(lineStart, markerIndex);
  if (/^[ \t]*$/.test(prefix)) {
    return prefix;
  }
  return null;
}

function consumeMarkerLine(text: string, indexAfterMarker: number): number {
  let i = indexAfterMarker;
  while (i < text.length && text[i] !== '\n' && text[i] !== '\r') {
    if (text[i] !== ' ' && text[i] !== '\t') {
      return indexAfterMarker;
    }
    i++;
  }
  if (text[i] === '\r') {
    i++;
  }
  if (text[i] === '\n') {
    i++;
  }
  return i;
}

function toInt(value: number | undefined, fallback: number): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
