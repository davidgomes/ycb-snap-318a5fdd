import dedent from 'ts-dedent';
import {Options, RuleType} from '../rules';
import {getBlockHtmlRanges} from '../utils/mdast';
import {IgnoreTypes} from '../utils/ignore-types';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';

type LinkStyleValue = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle?: LinkStyleValue = 'no-change';
  imageStyle?: LinkStyleValue = 'no-change';
}

type Range = {
  start: number,
  end: number,
};

type WikiSpan = {
  start: number,
  end: number,
  isEmbed: boolean,
  parts: string[],
};

type MarkdownSpan = {
  start: number,
  end: number,
  isImage: boolean,
  labelRaw: string,
  destinationRaw: string,
  hasTitle: boolean,
  hasNewline: boolean,
};

type Replacement = {
  start: number,
  end: number,
  text: string,
};

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [
        IgnoreTypes.yaml,
        IgnoreTypes.code,
        IgnoreTypes.inlineCode,
        IgnoreTypes.math,
        IgnoreTypes.inlineMath,
        IgnoreTypes.templaterCommand,
        IgnoreTypes.table,
      ],
    });
  }

  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }

  apply(text: string, options: LinkStyleOptions): string {
    const linkStyle = options.linkStyle ?? 'no-change';
    const imageStyle = options.imageStyle ?? 'no-change';
    if (linkStyle === 'no-change' && imageStyle === 'no-change') {
      return text;
    }

    const hardSkips = mergeRanges([
      ...collectCommentRanges(text),
      ...(text.includes('<') ? htmlRanges(text) : []),
    ]);
    const markdownSpans = collectMarkdownSpans(text, hardSkips);
    const wikiSkips = mergeRanges([
      ...hardSkips,
      ...markdownSpans.filter((span) => span.hasNewline).map((span) => ({start: span.start, end: span.end})),
    ]);
    const wikiSpans = collectWikiSpans(text, wikiSkips);

    const replacements: Replacement[] = [];
    for (const span of wikiSpans) {
      const converted = convertWiki(span, linkStyle, imageStyle);
      if (converted === null) {
        continue;
      }
      const original = text.slice(span.start, span.end);
      if (converted === original || duplicatesPlaceholder(original, converted)) {
        continue;
      }
      replacements.push({start: span.start, end: span.end, text: converted});
    }

    for (const span of markdownSpans) {
      if (span.hasNewline || overlapsAny(span.start, span.end, wikiSpans)) {
        continue;
      }
      const converted = convertMarkdown(span, linkStyle, imageStyle);
      if (converted === null) {
        continue;
      }
      const original = text.slice(span.start, span.end);
      if (converted === original || duplicatesPlaceholder(original, converted)) {
        continue;
      }
      replacements.push({start: span.start, end: span.end, text: converted});
    }

    return renderReplacements(text, replacements);
  }

  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Wiki links and embeds become markdown when both styles are `markdown`. Numeric embed sizes are dropped and heading links use their default display text',
        before: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          ![[f.png]]
          ![[f.png|300]]
          ![[f.png|300x200]]
          ![[f.png|caption]]
        `,
        after: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          ![f.png](f.png)
          ![f.png](f.png)
          ![f.png](f.png)
          ![caption](f.png)
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Inline markdown links and images become wiki links and embeds when both styles are `wiki`. External targets, titles, and multiline links stay as they are',
        before: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          [My Page]( <My Page> )
          [d](file(1).md)
          [d](https://example.com)
          [d](t "title")
          ![f.png](f.png)
          ![](f.png)
          ![caption](f.png)
          ![alt](https://example.com/a.png)
        `,
        after: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          [[My Page]]
          [[file(1).md|d]]
          [d](https://example.com)
          [d](t "title")
          ![[f.png]]
          ![[f.png]]
          ![[f.png|caption]]
          ![alt](https://example.com/a.png)
        `,
        options: {
          linkStyle: 'wiki',
          imageStyle: 'wiki',
        },
      }),
    ];
  }

  get optionBuilders(): OptionBuilderBase<LinkStyleOptions>[] {
    return [
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.linkStyle.name',
        descriptionKey: 'rules.link-style.linkStyle.description',
        optionsKey: 'linkStyle',
        records: [
          {
            value: 'no-change',
            description: 'Leaves links as they are',
          },
          {
            value: 'markdown',
            description: 'Converts wiki links to markdown links',
          },
          {
            value: 'wiki',
            description: 'Converts markdown links to wiki links',
          },
        ],
      }),
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.imageStyle.name',
        descriptionKey: 'rules.link-style.imageStyle.description',
        optionsKey: 'imageStyle',
        records: [
          {
            value: 'no-change',
            description: 'Leaves embeds and images as they are',
          },
          {
            value: 'markdown',
            description: 'Converts wiki embeds to markdown images',
          },
          {
            value: 'wiki',
            description: 'Converts markdown images to wiki embeds',
          },
        ],
      }),
    ];
  }
}

function htmlRanges(text: string): Range[] {
  return getBlockHtmlRanges(text).map((range) => ({start: range.startIndex, end: range.endIndex}));
}

function isNewline(char: string): boolean {
  return char === '\n' || char === '\r';
}

function isEscaped(text: string, index: number): boolean {
  let count = 0;
  for (let cursor = index - 1; cursor >= 0 && text[cursor] === '\\'; cursor--) {
    count++;
  }
  return count % 2 === 1;
}

function unescapeMarkdown(value: string): string {
  let result = '';
  for (let index = 0; index < value.length; index++) {
    if (value[index] === '\\' && index + 1 < value.length) {
      result += value[index + 1];
      index++;
      continue;
    }
    result += value[index];
  }
  return result;
}

function isEmbedSize(value: string): boolean {
  return /^\d+$/.test(value) || /^\d+x\d+$/.test(value);
}

function defaultHeadingDisplay(target: string): string | null {
  const hashIndex = target.indexOf('#');
  if (hashIndex === -1) {
    return null;
  }

  const page = target.slice(0, hashIndex);
  const segments = target.slice(hashIndex + 1).split('#');
  // Block references (`#^id`) and empty heading segments keep the raw target as their display.
  if (segments.length === 0 || segments.some((segment) => segment === '' || segment.startsWith('^'))) {
    return null;
  }
  if (page === '') {
    return segments.join(' > ');
  }
  return [page, ...segments].join(' > ');
}

function duplicatesPlaceholder(original: string, replacement: string): boolean {
  const count = (value: string): Map<string, number> => {
    const counts = new Map<string, number>();
    for (const match of value.matchAll(/\{[A-Z0-9_]+\}/g)) {
      counts.set(match[0], (counts.get(match[0]) ?? 0) + 1);
    }
    return counts;
  };

  const originalCounts = count(original);
  for (const [placeholder, amount] of count(replacement)) {
    if (amount > (originalCounts.get(placeholder) ?? 0)) {
      return true;
    }
  }
  return false;
}

function skipAsciiWhitespace(text: string, index: number): {end: number, hasNewline: boolean} {
  let cursor = index;
  let hasNewline = false;
  while (cursor < text.length) {
    const char = text[cursor];
    if (char === ' ' || char === '\t') {
      cursor++;
      continue;
    }
    if (isNewline(char)) {
      hasNewline = true;
      cursor++;
      continue;
    }
    break;
  }
  return {end: cursor, hasNewline};
}

function parseLabel(text: string, openBracket: number): {end: number, raw: string, hasNewline: boolean} | null {
  let depth = 1;
  let raw = '';
  let hasNewline = false;
  let cursor = openBracket + 1;

  while (cursor < text.length) {
    const char = text[cursor];
    if (char === '\\' && cursor + 1 < text.length) {
      if (isNewline(text[cursor + 1])) {
        hasNewline = true;
      }
      raw += char + text[cursor + 1];
      cursor += 2;
      continue;
    }
    if (isNewline(char)) {
      hasNewline = true;
      raw += char;
      cursor++;
      continue;
    }
    if (char === '[') {
      depth++;
      raw += char;
      cursor++;
      continue;
    }
    if (char === ']') {
      depth--;
      if (depth === 0) {
        return {end: cursor, raw, hasNewline};
      }
      raw += char;
      cursor++;
      continue;
    }
    raw += char;
    cursor++;
  }

  return null;
}

function parseTitle(text: string, index: number): {end: number, hasNewline: boolean} | null {
  const opener = text[index];
  const closer = opener === '(' ? ')' : opener;
  if (opener !== '"' && opener !== '\'' && opener !== '(') {
    return null;
  }

  let cursor = index + 1;
  let hasNewline = false;
  while (cursor < text.length) {
    const char = text[cursor];
    if (isNewline(char)) {
      hasNewline = true;
      cursor++;
      continue;
    }
    if (char === '\\' && cursor + 1 < text.length) {
      if (isNewline(text[cursor + 1])) {
        hasNewline = true;
      }
      cursor += 2;
      continue;
    }
    if (char === closer) {
      return {end: cursor + 1, hasNewline};
    }
    cursor++;
  }

  return null;
}

function parseDestination(text: string, index: number): {end: number, raw: string, hasNewline: boolean} | null {
  const skipped = skipAsciiWhitespace(text, index);
  let hasNewline = skipped.hasNewline;
  let cursor = skipped.end;
  if (cursor >= text.length) {
    return null;
  }

  if (text[cursor] === '<') {
    cursor++;
    let raw = '';
    let closed = false;
    while (cursor < text.length) {
      const char = text[cursor];
      if (isNewline(char)) {
        return null;
      }
      if (char === '\\' && cursor + 1 < text.length) {
        if (isNewline(text[cursor + 1])) {
          return null;
        }
        raw += char + text[cursor + 1];
        cursor += 2;
        continue;
      }
      if (char === '>') {
        closed = true;
        cursor++;
        break;
      }
      raw += char;
      cursor++;
    }
    if (!closed) {
      return null;
    }
    return {end: cursor, raw, hasNewline};
  }

  let raw = '';
  let depth = 0;
  while (cursor < text.length) {
    const char = text[cursor];
    if (isNewline(char) || char === ' ' || char === '\t') {
      break;
    }
    if (char === '\\' && cursor + 1 < text.length) {
      if (isNewline(text[cursor + 1])) {
        hasNewline = true;
        break;
      }
      raw += char + text[cursor + 1];
      cursor += 2;
      continue;
    }
    if (char === '(') {
      depth++;
      raw += char;
      cursor++;
      continue;
    }
    if (char === ')') {
      if (depth === 0) {
        break;
      }
      depth--;
      raw += char;
      cursor++;
      continue;
    }
    raw += char;
    cursor++;
  }

  if (depth !== 0) {
    return null;
  }
  return {end: cursor, raw, hasNewline};
}

function tryParseWiki(text: string, index: number): WikiSpan | null {
  let cursor = index;
  let isEmbed = false;
  if (text[cursor] === '!') {
    if (isEscaped(text, cursor) || text[cursor + 1] !== '[' || text[cursor + 2] !== '[') {
      return null;
    }
    isEmbed = true;
    cursor++;
  } else if (text[cursor] !== '[' || text[cursor + 1] !== '[' || isEscaped(text, cursor)) {
    return null;
  }

  let scan = cursor + 2;
  const parts: string[] = [];
  let current = '';
  while (scan < text.length) {
    const char = text[scan];
    if (isNewline(char) || char === '[') {
      return null;
    }
    if (char === '|') {
      if (current.length === 0 || parts.length >= 2) {
        return null;
      }
      parts.push(current);
      current = '';
      scan++;
      continue;
    }
    if (char === ']') {
      if (text[scan + 1] !== ']') {
        return null;
      }
      if (current.length === 0) {
        return null;
      }
      parts.push(current);
      return {
        start: index,
        end: scan + 2,
        isEmbed,
        parts,
      };
    }
    current += char;
    scan++;
  }

  return null;
}

function tryParseMarkdown(text: string, index: number): MarkdownSpan | null {
  let cursor = index;
  let isImage = false;
  if (text[cursor] === '!') {
    if (isEscaped(text, cursor) || text[cursor + 1] !== '[') {
      return null;
    }
    isImage = true;
    cursor++;
  }

  if (text[cursor] !== '[' || isEscaped(text, cursor)) {
    return null;
  }

  const label = parseLabel(text, cursor);
  if (!label) {
    return null;
  }

  let scan = label.end + 1;
  if (text[scan] !== '(') {
    return null;
  }

  const destination = parseDestination(text, scan + 1);
  if (!destination) {
    return null;
  }

  let hasNewline = label.hasNewline || destination.hasNewline;
  scan = destination.end;
  const afterDestination = skipAsciiWhitespace(text, scan);
  hasNewline = hasNewline || afterDestination.hasNewline;
  scan = afterDestination.end;

  let hasTitle = false;
  if (scan < text.length && (text[scan] === '"' || text[scan] === '\'' || text[scan] === '(')) {
    const title = parseTitle(text, scan);
    if (!title) {
      return null;
    }
    hasTitle = true;
    hasNewline = hasNewline || title.hasNewline;
    const afterTitle = skipAsciiWhitespace(text, title.end);
    hasNewline = hasNewline || afterTitle.hasNewline;
    scan = afterTitle.end;
  }

  if (text[scan] !== ')') {
    return null;
  }

  return {
    start: index,
    end: scan + 1,
    isImage,
    labelRaw: label.raw,
    destinationRaw: destination.raw,
    hasTitle,
    hasNewline,
  };
}

function convertWiki(span: WikiSpan, linkStyle: LinkStyleValue, imageStyle: LinkStyleValue): string | null {
  const style = span.isEmbed ? imageStyle : linkStyle;
  if (style !== 'markdown') {
    return null;
  }

  const target = span.parts[0];
  let explicitDisplay: string | null = null;
  if (span.isEmbed) {
    if (span.parts.length === 2) {
      explicitDisplay = isEmbedSize(span.parts[1]) ? null : span.parts[1];
    } else if (span.parts.length === 3) {
      if (!isEmbedSize(span.parts[2])) {
        return null;
      }
      explicitDisplay = isEmbedSize(span.parts[1]) ? null : span.parts[1];
    } else if (span.parts.length !== 1) {
      return null;
    }
  } else if (span.parts.length === 2) {
    explicitDisplay = span.parts[1];
  } else if (span.parts.length !== 1) {
    return null;
  }

  const visible = explicitDisplay === null ? (defaultHeadingDisplay(target) ?? target) : explicitDisplay;
  return `${span.isEmbed ? '!' : ''}[${visible}](${target})`;
}

function convertMarkdown(span: MarkdownSpan, linkStyle: LinkStyleValue, imageStyle: LinkStyleValue): string | null {
  if (span.hasNewline || span.hasTitle) {
    return null;
  }

  const style = span.isImage ? imageStyle : linkStyle;
  if (style !== 'wiki') {
    return null;
  }

  const label = unescapeMarkdown(span.labelRaw);
  const target = unescapeMarkdown(span.destinationRaw);
  if (target.includes('://')) {
    return null;
  }

  const headingDisplay = defaultHeadingDisplay(target);
  const omitDisplay = label === target ||
    (headingDisplay !== null && label === headingDisplay) ||
    (span.isImage && label === '');
  if (omitDisplay) {
    return `${span.isImage ? '!' : ''}[[${target}]]`;
  }
  return `${span.isImage ? '!' : ''}[[${target}|${label}]]`;
}

function collectCommentRanges(text: string): Range[] {
  const ranges: Range[] = [];
  const commentRegex = /%%[\s\S]*?%%/g;
  let match: RegExpExecArray | null;
  while ((match = commentRegex.exec(text)) !== null) {
    ranges.push({start: match.index, end: match.index + match[0].length});
  }
  return ranges;
}

function mergeRanges(ranges: Range[]): Range[] {
  if (ranges.length === 0) {
    return [];
  }

  const sorted = ranges.map((range) => ({start: range.start, end: range.end}))
      .sort((left, right) => left.start - right.start || left.end - right.end);
  const merged: Range[] = [sorted[0]];
  for (let index = 1; index < sorted.length; index++) {
    const last = merged[merged.length - 1];
    const current = sorted[index];
    if (current.start <= last.end) {
      last.end = Math.max(last.end, current.end);
    } else {
      merged.push(current);
    }
  }
  return merged;
}

function rangeContaining(index: number, ranges: Range[]): Range | null {
  for (const range of ranges) {
    if (range.start > index) {
      return null;
    }
    if (index < range.end) {
      return range;
    }
  }
  return null;
}

function overlaps(start: number, end: number, ranges: Range[]): boolean {
  for (const range of ranges) {
    if (range.start >= end) {
      return false;
    }
    if (range.end > start) {
      return true;
    }
  }
  return false;
}

function overlapsAny(start: number, end: number, spans: {start: number, end: number}[]): boolean {
  return spans.some((span) => start < span.end && span.start < end);
}

function collectWikiSpans(text: string, skipRanges: Range[]): WikiSpan[] {
  const spans: WikiSpan[] = [];
  let index = 0;
  while (index < text.length) {
    const skip = rangeContaining(index, skipRanges);
    if (skip) {
      index = skip.end;
      continue;
    }

    const wiki = tryParseWiki(text, index);
    if (wiki && wiki.end > index && !overlaps(wiki.start, wiki.end, skipRanges)) {
      spans.push(wiki);
      index = wiki.end;
      continue;
    }
    index++;
  }
  return spans;
}

function collectMarkdownSpans(text: string, hardSkips: Range[]): MarkdownSpan[] {
  const spans: MarkdownSpan[] = [];
  let index = 0;
  while (index < text.length) {
    const skip = rangeContaining(index, hardSkips);
    if (skip) {
      index = skip.end;
      continue;
    }

    const markdown = tryParseMarkdown(text, index);
    if (markdown && markdown.end > index && (markdown.hasNewline || !overlaps(markdown.start, markdown.end, hardSkips))) {
      spans.push(markdown);
      index = markdown.end;
      continue;
    }
    index++;
  }
  return spans;
}

function renderReplacements(text: string, replacements: Replacement[]): string {
  replacements.sort((left, right) => left.start - right.start);
  let result = '';
  let cursor = 0;
  for (const replacement of replacements) {
    if (replacement.start < cursor || replacement.end < replacement.start) {
      continue;
    }
    result += text.slice(cursor, replacement.start);
    result += replacement.text;
    cursor = replacement.end;
  }
  result += text.slice(cursor);
  return result;
}
