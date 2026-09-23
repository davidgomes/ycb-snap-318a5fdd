import {IgnoreTypes, IgnoreType} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValue = 'no-change' | 'markdown' | 'wiki';

const obsidianCommentIgnore: IgnoreType = {
  replaceAction: /%%[\s\S]*?%%/g,
  placeholder: '{LINK_STYLE_OBSIDIAN_COMMENT_PLACEHOLDER}',
};

const embedSizeRegex = /^\d+(?:x\d+)?$/;

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValue = 'no-change';
  imageStyle: LinkStyleValue = 'no-change';
}

type WikiMatch = {
  end: number;
  isEmbed: boolean;
  target: string;
  displays: string[];
};

type MarkdownMatch = {
  end: number;
  isImage: boolean;
  label: string;
  destination: string;
  hasTitle: boolean;
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
        IgnoreTypes.html,
        IgnoreTypes.templaterCommand,
        obsidianCommentIgnore,
        IgnoreTypes.table,
      ],
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    if (options.linkStyle === 'no-change' && options.imageStyle === 'no-change') {
      return text;
    }

    let result = '';
    let index = 0;
    while (index < text.length) {
      const match = matchAt(text, index, options);
      if (match) {
        result += match.replacement;
        index = match.end;
      } else {
        result += text[index];
        index++;
      }
    }

    return result;
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Wiki links and embeds become markdown links and images when both styles are `markdown`',
        before: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          ![[f.png]]
          ![[f.png|300]]
          ![[f.png|300x200]]
          ![[f.png|alt|300]]
        `,
        after: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          ![f.png](f.png)
          ![f.png](f.png)
          ![f.png](f.png)
          ![alt](f.png)
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Markdown links and images become wiki links and embeds when both styles are `wiki`',
        before: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          ![alt](f.png)
          ![](f.png)
          ![f.png](f.png)
          [external](https://example.com)
          [kept](t "title")
        `,
        after: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          ![[f.png|alt]]
          ![[f.png]]
          ![[f.png]]
          [external](https://example.com)
          [kept](t "title")
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
        records: styleRecords(),
      }),
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.imageStyle.name',
        descriptionKey: 'rules.link-style.imageStyle.description',
        optionsKey: 'imageStyle',
        records: styleRecords(),
      }),
    ];
  }
}

function styleRecords(): {value: LinkStyleValue, description: string}[] {
  return [
    {
      value: 'no-change',
      description: 'Leaves links or images in their current style',
    },
    {
      value: 'markdown',
      description: 'Converts wiki links or embeds to markdown links or images',
    },
    {
      value: 'wiki',
      description: 'Converts markdown links or images to wiki links or embeds',
    },
  ];
}

function matchAt(text: string, index: number, options: LinkStyleOptions): {end: number, replacement: string} | null {
  const current = text[index];
  if (current === '!' && !isEscaped(text, index) && text[index + 1] === '[') {
    if (text[index + 2] === '[') {
      const wiki = tryParseWiki(text, index, true);
      if (wiki) {
        return finishWiki(text, wiki, options);
      }
    }

    const image = tryParseMarkdown(text, index, true);
    if (image) {
      return finishMarkdown(text, image, options);
    }

    return null;
  }

  if (current === '[' && !isEscaped(text, index)) {
    if (text[index + 1] === '[') {
      const wiki = tryParseWiki(text, index, false);
      if (wiki) {
        return finishWiki(text, wiki, options);
      }
    }

    const link = tryParseMarkdown(text, index, false);
    if (link) {
      return finishMarkdown(text, link, options);
    }
  }

  return null;
}

function finishWiki(text: string, wiki: WikiMatch, options: LinkStyleOptions): {end: number, replacement: string} {
  const original = text.slice(wiki.start, wiki.end);
  const converted = convertWiki(wiki, options);
  return {end: wiki.end, replacement: converted ?? original};
}

function finishMarkdown(text: string, markdown: MarkdownMatch, options: LinkStyleOptions): {end: number, replacement: string} {
  const original = text.slice(markdown.start, markdown.end);
  const converted = convertMarkdown(markdown, options);
  return {end: markdown.end, replacement: converted ?? original};
}

function convertWiki(wiki: WikiMatch, options: LinkStyleOptions): string | null {
  if (wiki.isEmbed) {
    if (options.imageStyle !== 'markdown') {
      return null;
    }

    const alt = embedAlt(wiki);
    if (alt === null) {
      return null;
    }

    return `![${alt}](${wiki.target})`;
  }

  if (options.linkStyle !== 'markdown') {
    return null;
  }

  const display = wiki.displays.length === 0 ? defaultLinkDisplay(wiki.target) : wiki.displays[0];
  return `[${display}](${wiki.target})`;
}

function convertMarkdown(markdown: MarkdownMatch, options: LinkStyleOptions): string | null {
  if (markdown.hasTitle || markdown.destination.includes('://')) {
    return null;
  }

  if (markdown.isImage) {
    if (options.imageStyle !== 'wiki') {
      return null;
    }

    if (shouldOmitImageAlt(markdown.label, markdown.destination)) {
      return `![[${markdown.destination}]]`;
    }

    return `![[${markdown.destination}|${markdown.label}]]`;
  }

  if (options.linkStyle !== 'wiki') {
    return null;
  }

  if (shouldOmitLinkDisplay(markdown.label, markdown.destination)) {
    return `[[${markdown.destination}]]`;
  }

  return `[[${markdown.destination}|${markdown.label}]]`;
}

function embedAlt(wiki: WikiMatch): string | null {
  const extras = wiki.displays;
  if (extras.length === 0) {
    return wiki.target;
  }

  const nonSize = extras.filter((part) => !embedSizeRegex.test(part));
  if (nonSize.length === 0) {
    return wiki.target;
  }

  if (nonSize.length === 1) {
    return nonSize[0];
  }

  return null;
}

function defaultLinkDisplay(target: string): string {
  return defaultHeadingDisplay(target) ?? target;
}

function defaultHeadingDisplay(target: string): string | null {
  const hashIndex = target.indexOf('#');
  if (hashIndex === -1) {
    return null;
  }

  const path = target.slice(0, hashIndex);
  const heading = target.slice(hashIndex + 1);
  if (heading.length === 0 || heading.startsWith('^')) {
    return null;
  }

  if (path.length === 0) {
    return heading;
  }

  return `${path} > ${heading}`;
}

function shouldOmitLinkDisplay(label: string, target: string): boolean {
  if (label === target) {
    return true;
  }

  const headingDisplay = defaultHeadingDisplay(target);
  return headingDisplay !== null && label === headingDisplay;
}

function shouldOmitImageAlt(alt: string, target: string): boolean {
  if (alt.length === 0 || alt === target) {
    return true;
  }

  const headingDisplay = defaultHeadingDisplay(target);
  return headingDisplay !== null && alt === headingDisplay;
}

function tryParseWiki(text: string, index: number, isEmbed: boolean): (WikiMatch & {start: number}) | null {
  let cursor = index;
  if (isEmbed) {
    if (text[cursor] !== '!' || isEscaped(text, cursor)) {
      return null;
    }
    cursor++;
  }

  if (text[cursor] !== '[' || text[cursor + 1] !== '[' || isEscaped(text, cursor)) {
    return null;
  }
  cursor += 2;

  const segments: string[] = [];
  let current = '';
  while (cursor < text.length) {
    const character = text[cursor];
    if (character === '\n' || character === '\r' || character === '[') {
      return null;
    }

    if (character === ']') {
      if (text[cursor + 1] !== ']') {
        return null;
      }

      segments.push(current);
      cursor += 2;
      if (segments[0].length === 0) {
        return null;
      }

      if (!isEmbed && segments.length > 2) {
        return null;
      }

      if (isEmbed && segments.length > 3) {
        return null;
      }

      return {
        start: index,
        end: cursor,
        isEmbed,
        target: segments[0],
        displays: segments.slice(1),
      };
    }

    if (character === '|') {
      segments.push(current);
      current = '';
      cursor++;
      continue;
    }

    current += character;
    cursor++;
  }

  return null;
}

function tryParseMarkdown(text: string, index: number, isImage: boolean): (MarkdownMatch & {start: number}) | null {
  let cursor = index;
  if (isImage) {
    if (text[cursor] !== '!' || isEscaped(text, cursor)) {
      return null;
    }
    cursor++;
  }

  if (text[cursor] !== '[' || isEscaped(text, cursor) || text[cursor + 1] === '[') {
    return null;
  }

  const label = parseLabel(text, cursor);
  if (!label) {
    return null;
  }

  if (text[label.end] !== '(') {
    return null;
  }

  const destination = parseDestination(text, label.end + 1);
  if (!destination) {
    return null;
  }

  return {
    start: index,
    end: destination.end,
    isImage,
    label: label.value,
    destination: destination.value,
    hasTitle: destination.hasTitle,
  };
}

function parseLabel(text: string, openBracket: number): {end: number, value: string} | null {
  let cursor = openBracket + 1;
  let depth = 1;
  let value = '';

  while (cursor < text.length) {
    const character = text[cursor];
    if (character === '\n' || character === '\r') {
      return null;
    }

    if (character === '\\') {
      if (cursor + 1 >= text.length) {
        return null;
      }

      const next = text[cursor + 1];
      if (next === '\n' || next === '\r') {
        return null;
      }

      value += next;
      cursor += 2;
      continue;
    }

    if (character === '[') {
      depth++;
      value += character;
      cursor++;
      continue;
    }

    if (character === ']') {
      depth--;
      if (depth === 0) {
        return {end: cursor + 1, value};
      }

      value += character;
      cursor++;
      continue;
    }

    value += character;
    cursor++;
  }

  return null;
}

function parseDestination(text: string, start: number): {end: number, value: string, hasTitle: boolean} | null {
  const afterSpace = skipInlineSpace(text, start);
  if (text[afterSpace] === '<') {
    return parseAngleDestination(text, afterSpace);
  }

  return parseBareDestination(text, start);
}

function parseAngleDestination(text: string, openingBracket: number): {end: number, value: string, hasTitle: boolean} | null {
  let cursor = openingBracket + 1;
  let value = '';
  while (cursor < text.length) {
    const character = text[cursor];
    if (character === '\n' || character === '\r') {
      return null;
    }

    if (character === '\\') {
      if (cursor + 1 >= text.length) {
        return null;
      }

      const next = text[cursor + 1];
      if (next === '\n' || next === '\r') {
        return null;
      }

      value += next;
      cursor += 2;
      continue;
    }

    if (character === '>') {
      cursor++;
      const afterDestination = skipInlineSpace(text, cursor);
      if (text[afterDestination] === ')') {
        return {end: afterDestination + 1, value, hasTitle: false};
      }

      const titleEnd = parseTitle(text, afterDestination);
      if (titleEnd === null) {
        return null;
      }

      const afterTitle = skipInlineSpace(text, titleEnd);
      if (containsLineBreak(text, titleEnd, afterTitle) || text[afterTitle] !== ')') {
        return null;
      }

      return {end: afterTitle + 1, value, hasTitle: true};
    }

    value += character;
    cursor++;
  }

  return null;
}

function parseBareDestination(text: string, start: number): {end: number, value: string, hasTitle: boolean} | null {
  let cursor = start;
  let depth = 1;
  let value = '';

  while (cursor < text.length) {
    const character = text[cursor];
    if (character === '\n' || character === '\r') {
      return null;
    }

    if (character === '\\') {
      if (cursor + 1 >= text.length) {
        return null;
      }

      const next = text[cursor + 1];
      if (next === '\n' || next === '\r') {
        return null;
      }

      value += next;
      cursor += 2;
      continue;
    }

    if (character === '(') {
      depth++;
      value += character;
      cursor++;
      continue;
    }

    if (character === ')') {
      depth--;
      if (depth === 0) {
        return {end: cursor + 1, value, hasTitle: false};
      }

      value += character;
      cursor++;
      continue;
    }

    if (depth === 1 && (character === ' ' || character === '\t')) {
      const afterSpace = skipInlineSpace(text, cursor);
      const titleEnd = parseTitle(text, afterSpace);
      if (titleEnd !== null) {
        const afterTitle = skipInlineSpace(text, titleEnd);
        if (!containsLineBreak(text, cursor, afterTitle) && text[afterTitle] === ')') {
          return {end: afterTitle + 1, value, hasTitle: true};
        }
      }
    }

    value += character;
    cursor++;
  }

  return null;
}

function parseTitle(text: string, index: number): number | null {
  const opener = text[index];
  if (opener !== '"' && opener !== '\'') {
    return null;
  }

  const closer = opener;
  let cursor = index + 1;
  while (cursor < text.length) {
    const character = text[cursor];
    if (character === '\n' || character === '\r') {
      return null;
    }

    if (character === '\\') {
      if (cursor + 1 >= text.length || text[cursor + 1] === '\n' || text[cursor + 1] === '\r') {
        return null;
      }

      cursor += 2;
      continue;
    }

    if (character === closer) {
      return cursor + 1;
    }

    cursor++;
  }

  return null;
}

function skipInlineSpace(text: string, index: number): number {
  let cursor = index;
  while (cursor < text.length && (text[cursor] === ' ' || text[cursor] === '\t')) {
    cursor++;
  }

  return cursor;
}

function containsLineBreak(text: string, start: number, end: number): boolean {
  for (let cursor = start; cursor < end; cursor++) {
    if (text[cursor] === '\n' || text[cursor] === '\r') {
      return true;
    }
  }

  return false;
}

function isEscaped(text: string, index: number): boolean {
  let slashCount = 0;
  for (let cursor = index - 1; cursor >= 0 && text[cursor] === '\\'; cursor--) {
    slashCount++;
  }

  return slashCount % 2 === 1;
}
