import {Options, RuleType} from '../rules';
import RuleBuilder, {BooleanOptionBuilder, DropdownOptionBuilder, ExampleBuilder, NumberOptionBuilder, OptionBuilderBase, TextAreaOptionBuilder, TextOptionBuilder} from './rule-builder';
import dedent from 'ts-dedent';

type ListStyle = 'bullet' | 'number';
type OrderedListStyle = 'always-one' | 'increment';

class AutoTocOptions implements Options {
  listStyle?: ListStyle = 'bullet';
  bulletMarker?: string = '-';
  orderedListStyle?: OrderedListStyle = 'always-one';
  indentSize?: number = 2;
  minLevel?: number = 2;
  maxLevel?: number = 6;
  title?: string = '';
  useExplicitIds?: boolean = false;
  stripFormattingInToc?: boolean = false;
  excludeHeadings?: string[] = [];
}

const startMarkerRegex = /<!--\s*toc\s*-->/i;
const endMarkerRegex = /<!--\s*\/toc\s*-->/i;

interface Heading {
  level: number;
  text: string;
}

function resolveDisplayText(text: string): string {
  return text
      .replace(/!\[\[[^\]]*\]\]/g, '')
      .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
      .replace(/\[\[([^\]|]*)\|([^\]]*)\]\]/g, '$2')
      .replace(/\[\[([^\]]*)\]\]/g, '$1')
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1');
}

function stripFormatting(text: string): string {
  return text
      .replace(/(\*\*|__|~~|==)(.+?)\1/g, '$2')
      .replace(/\*([^*]+)\*/g, '$1')
      .replace(/(^|[^\w])_([^_]+)_(?=[^\w]|$)/g, '$1$2')
      .replace(/`([^`]*)`/g, '$1')
      .replace(/\s+/g, ' ')
      .trim();
}

function slugify(text: string): string {
  return text
      .toLowerCase()
      .replace(/ /g, '-')
      .replace(/[^a-z0-9\-_]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-+|-+$/g, '');
}

function isExcluded(text: string, excludeHeadings: string[]): boolean {
  for (const pattern of excludeHeadings) {
    const trimmed = pattern.trim();
    if (trimmed === '') {
      continue;
    }

    if (trimmed.length > 1 && trimmed.startsWith('/') && trimmed.endsWith('/')) {
      try {
        if (new RegExp(trimmed.slice(1, -1), 'i').test(text)) {
          return true;
        }
      } catch {
        continue;
      }
    } else if (trimmed.toLowerCase() === text.toLowerCase()) {
      return true;
    }
  }

  return false;
}

function findHeadings(text: string, regionStart: number, regionEnd: number): Heading[] {
  const headings: Heading[] = [];
  const lines = text.split('\n');
  let offset = 0;
  let fence: string | null = null;
  let inMath = false;
  let inYaml = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineStart = offset;
    offset += line.length + 1;

    if (i === 0 && line.trimEnd() === '---') {
      inYaml = true;
      continue;
    }
    if (inYaml) {
      if (line.trimEnd() === '---' || line.trimEnd() === '...') {
        inYaml = false;
      }
      continue;
    }

    if (fence !== null) {
      const closing = line.match(/^\s*(`{3,}|~{3,})\s*$/);
      if (closing && closing[1][0] === fence[0] && closing[1].length >= fence.length) {
        fence = null;
      }
      continue;
    }
    const opening = line.match(/^\s*(`{3,}|~{3,})/);
    if (opening) {
      fence = opening[1];
      continue;
    }

    if (inMath) {
      if (line.includes('$$')) {
        inMath = false;
      }
      continue;
    }
    const trimmed = line.trim();
    if (trimmed.startsWith('$$')) {
      if (!(trimmed.length >= 4 && trimmed.endsWith('$$'))) {
        inMath = true;
      }
      continue;
    }

    if (lineStart >= regionStart && lineStart < regionEnd) {
      continue;
    }

    const match = line.match(/^ {0,3}(#{1,6})[ \t]+(.*)$/);
    if (!match) {
      continue;
    }

    const headingText = match[2].replace(/[ \t]+#+[ \t]*$/, '').replace(/^#+[ \t]*$/, '').trim();
    headings.push({level: match[1].length, text: headingText});
  }

  return headings;
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
    const startMatch = startMarkerRegex.exec(text);
    if (!startMatch) {
      return text;
    }

    const startIndex = startMatch.index;
    const startEnd = startIndex + startMatch[0].length;
    const afterStart = text.substring(startEnd);
    const endMatch = endMarkerRegex.exec(afterStart);

    let endMarker = '<!-- /toc -->';
    let rest = afterStart;
    let regionEnd = startEnd;
    if (endMatch) {
      endMarker = endMatch[0];
      rest = afterStart.substring(endMatch.index + endMatch[0].length);
      regionEnd = startEnd + endMatch.index + endMatch[0].length;
    }

    const minLevel = Number(options.minLevel ?? 2);
    const maxLevel = Number(options.maxLevel ?? 6);
    const indentSize = Number(options.indentSize ?? 2);
    const excludeHeadings = options.excludeHeadings ?? [];

    const headings = findHeadings(text, startIndex, regionEnd).filter((heading) => heading.level >= minLevel && heading.level <= maxLevel);

    const entries: {level: number, label: string, anchor: string}[] = [];
    const anchorCounts = new Map<string, number>();
    for (const heading of headings) {
      let headingText = heading.text;
      let explicitId: string | null = null;
      const idMatch = headingText.match(/\s*\{#([^}\s]+)\}\s*$/);
      if (idMatch) {
        headingText = headingText.substring(0, idMatch.index).trim();
        if (options.useExplicitIds) {
          explicitId = idMatch[1];
        }
      }

      const plainText = stripFormatting(resolveDisplayText(headingText));
      if (isExcluded(plainText, excludeHeadings) || isExcluded(headingText, excludeHeadings)) {
        continue;
      }

      const baseAnchor = explicitId ?? slugify(plainText);
      const count = anchorCounts.get(baseAnchor) ?? 0;
      anchorCounts.set(baseAnchor, count + 1);
      const anchor = count === 0 ? baseAnchor : `${baseAnchor}-${count}`;

      const label = options.stripFormattingInToc ? plainText : headingText;
      entries.push({level: heading.level, label, anchor});
    }

    const baseLevel = entries.length > 0 ? Math.min(...entries.map((entry) => entry.level)) : minLevel;
    let counter = 0;
    const items = entries.map((entry) => {
      counter++;
      let marker: string;
      if (options.listStyle === 'number') {
        marker = options.orderedListStyle === 'increment' ? `${counter}.` : '1.';
      } else {
        marker = options.bulletMarker || '-';
      }

      const indent = ' '.repeat(Math.max(0, (entry.level - baseLevel) * indentSize));
      return `${indent}${marker} [${entry.label}](#${entry.anchor})`;
    });

    let tocBlock = startMatch[0] + '\n\n';
    const title = (options.title ?? '').trim();
    if (title !== '') {
      tocBlock += title + '\n\n';
    }
    if (items.length > 0) {
      tocBlock += items.join('\n') + '\n\n';
    }
    tocBlock += endMarker;

    const remaining = rest.replace(/^[ \t]*\n(?:[ \t]*\n)*/, '');
    if (remaining.trim() === '') {
      return text.substring(0, startIndex) + tocBlock + rest.replace(/^[ \t]+/, '');
    }

    return text.substring(0, startIndex) + tocBlock + '\n\n' + remaining;
  }
  get exampleBuilders(): ExampleBuilder<AutoTocOptions>[] {
    return [
      new ExampleBuilder({
        description: 'A table of contents is generated where the `<!-- toc -->` marker is',
        before: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          ## First Section
          ${''}
          ### Sub Section
          ${''}
          ## Second Section
        `,
        after: dedent`
          # Title
          ${''}
          <!-- toc -->
          ${''}
          - [First Section](#first-section)
            - [Sub Section](#sub-section)
          - [Second Section](#second-section)
          ${''}
          <!-- /toc -->
          ${''}
          ## First Section
          ${''}
          ### Sub Section
          ${''}
          ## Second Section
        `,
      }),
      new ExampleBuilder({
        description: 'An existing table of contents is updated and duplicate anchors are made unique',
        before: dedent`
          <!-- toc -->
          - [Old](#old)
          <!-- /toc -->
          ## Notes
          ## Notes
        `,
        after: dedent`
          <!-- toc -->
          ${''}
          - [Notes](#notes)
          - [Notes](#notes-1)
          ${''}
          <!-- /toc -->
          ${''}
          ## Notes
          ## Notes
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
          {value: 'bullet', description: 'Use a bullet list'},
          {value: 'number', description: 'Use a numbered list'},
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
          {value: 'always-one', description: 'Every item uses `1.`'},
          {value: 'increment', description: 'Items are numbered incrementally across the whole list'},
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
