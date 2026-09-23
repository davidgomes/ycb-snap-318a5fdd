import {IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValue = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle?: LinkStyleValue = 'no-change';
  imageStyle?: LinkStyleValue = 'no-change';
}

const ASCII_PUNCTUATION = new Set('!"#$%&\'()*+,-./:;<=>?@[\\]^_`{|}~');

type ParsedInline = {
  end: number,
  label: string,
  destination: string,
  hasTitle: boolean,
  hasNewline: boolean,
};

type ParsedWiki = {
  end: number,
  target: string,
  display: string | null,
};

const linkStyleRecords: {value: LinkStyleValue, description: string}[] = [
  {
    value: 'no-change',
    description: 'Leaves the current syntax unchanged',
  },
  {
    value: 'markdown',
    description: 'Converts wiki syntax to markdown syntax',
  },
  {
    value: 'wiki',
    description: 'Converts markdown syntax to wiki syntax',
  },
];

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
        IgnoreTypes.obsidianMultiLineComments,
        IgnoreTypes.table,
        {replaceAction: /%%[\s\S]*?%%/g, placeholder: '{LINK_STYLE_OBSIDIAN_COMMENT_PLACEHOLDER}'},
      ],
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    const convertWikiLinks = options.linkStyle === 'markdown';
    const convertMarkdownLinks = options.linkStyle === 'wiki';
    const convertWikiImages = options.imageStyle === 'markdown';
    const convertMarkdownImages = options.imageStyle === 'wiki';
    if (!convertWikiLinks && !convertMarkdownLinks && !convertWikiImages && !convertMarkdownImages) {
      return text;
    }

    return convertLinkStyle(text, convertWikiLinks, convertMarkdownLinks, convertWikiImages, convertMarkdownImages);
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Wiki links are converted to markdown links when `Link Style` is `markdown`, including the default heading display',
        before: dedent`
          See [[my-page]] and [[my-page|My Page]].
          Read [[my-page#introduction]] and [[#conclusion]].
        `,
        after: dedent`
          See [my-page](my-page) and [My Page](my-page).
          Read [my-page > introduction](my-page#introduction) and [conclusion](#conclusion).
        `,
        options: {
          linkStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Wiki embeds are converted to markdown images when `Image Style` is `markdown`, and numeric sizes are dropped',
        before: dedent`
          ![[photo.png]]
          ![[photo.png|A nice photo]]
          ![[photo.png|300]]
          ![[photo.png|300x200]]
        `,
        after: dedent`
          ![photo.png](photo.png)
          ![A nice photo](photo.png)
          ![photo.png](photo.png)
          ![photo.png](photo.png)
        `,
        options: {
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Markdown links are converted to wiki links when `Link Style` is `wiki`. External targets, titles, and display text that matches the target or default heading display are left without an alias or unchanged',
        before: dedent`
          See [My Page](my-page) and [my-page](my-page).
          Read [my-page > intro](my-page#intro) and [conclusion](#conclusion).
          Visit [Google](https://google.com) and [Doc](page "Title").
        `,
        after: dedent`
          See [[my-page|My Page]] and [[my-page]].
          Read [[my-page#intro]] and [[#conclusion]].
          Visit [Google](https://google.com) and [Doc](page "Title").
        `,
        options: {
          linkStyle: 'wiki',
        },
      }),
      new ExampleBuilder({
        description: 'Markdown images are converted to wiki embeds when `Image Style` is `wiki`',
        before: dedent`
          ![A photo](photo.png)
          ![photo.png](photo.png)
          ![](photo.png)
        `,
        after: dedent`
          ![[photo.png|A photo]]
          ![[photo.png]]
          ![[photo.png]]
        `,
        options: {
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
        records: linkStyleRecords,
      }),
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.imageStyle.name',
        descriptionKey: 'rules.link-style.imageStyle.description',
        optionsKey: 'imageStyle',
        records: linkStyleRecords,
      }),
    ];
  }
}

function convertLinkStyle(text: string, convertWikiLinks: boolean, convertMarkdownLinks: boolean, convertWikiImages: boolean, convertMarkdownImages: boolean): string {
  const out: string[] = [];
  let i = 0;
  while (i < text.length) {
    if (startsWith(text, i, '![[') && !isEscaped(text, i)) {
      const wiki = parseWiki(text, i, true);
      if (wiki) {
        out.push(convertWikiImages ? formatMarkdownLink(wiki.target, wikiDisplay(wiki), true) : text.slice(i, wiki.end));
        i = wiki.end;
        continue;
      }
    }

    if (startsWith(text, i, '[[') && !isEscaped(text, i)) {
      const wiki = parseWiki(text, i, false);
      if (wiki) {
        out.push(convertWikiLinks ? formatMarkdownLink(wiki.target, wikiDisplay(wiki), false) : text.slice(i, wiki.end));
        i = wiki.end;
        continue;
      }
    }

    if (startsWith(text, i, '![') && !isEscaped(text, i)) {
      const markdown = parseInlineLink(text, i + 1);
      if (markdown) {
        out.push(convertMarkdownImages && canConvertMarkdown(markdown) ? formatWikiLink(markdown.destination, markdown.label, true) : text.slice(i, markdown.end));
        i = markdown.end;
        continue;
      }
    }

    if (text[i] === '[' && !isEscaped(text, i)) {
      const markdown = parseInlineLink(text, i);
      if (markdown) {
        out.push(convertMarkdownLinks && canConvertMarkdown(markdown) ? formatWikiLink(markdown.destination, markdown.label, false) : text.slice(i, markdown.end));
        i = markdown.end;
        continue;
      }
    }

    out.push(text[i]);
    i++;
  }

  return out.join('');
}

function startsWith(text: string, index: number, value: string): boolean {
  return text.startsWith(value, index);
}

function isEscaped(text: string, index: number): boolean {
  let slashes = 0;
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) {
    slashes++;
  }

  return slashes % 2 === 1;
}

function isNewline(char: string): boolean {
  return char === '\n' || char === '\r';
}

function wikiDisplay(wiki: ParsedWiki): string {
  if (wiki.display !== null) {
    return wiki.display;
  }

  return defaultHeadingDisplay(wiki.target) ?? wiki.target;
}

function defaultHeadingDisplay(target: string): string | null {
  const hashIndex = target.indexOf('#');
  if (hashIndex === -1) {
    return null;
  }

  const page = target.slice(0, hashIndex);
  const fragmentParts = target.slice(hashIndex + 1).split('#');
  if (page.length === 0) {
    return fragmentParts.join(' > ');
  }

  return [page, ...fragmentParts].join(' > ');
}

function formatMarkdownLink(target: string, display: string, isImage: boolean): string {
  return `${isImage ? '!' : ''}[${display}](${target})`;
}

function formatWikiLink(target: string, display: string, isImage: boolean): string {
  const prefix = isImage ? '!' : '';
  if (shouldOmitDisplay(display, target, isImage)) {
    return `${prefix}[[${target}]]`;
  }

  return `${prefix}[[${target}|${display}]]`;
}

function shouldOmitDisplay(display: string, target: string, isImage: boolean): boolean {
  if (isImage && display.length === 0) {
    return true;
  }

  if (display === target) {
    return true;
  }

  const headingDisplay = defaultHeadingDisplay(target);
  return headingDisplay !== null && display === headingDisplay;
}

function canConvertMarkdown(link: ParsedInline): boolean {
  return !link.hasTitle && !link.hasNewline && link.destination.length > 0 && !link.destination.includes('://');
}

function isEmbedSize(display: string): boolean {
  return /^\d+$/.test(display) || /^\d+x\d+$/.test(display);
}

function parseWiki(text: string, start: number, isImage: boolean): ParsedWiki | null {
  let i = start;
  if (isImage) {
    i++;
  }

  if (text[i] !== '[' || text[i + 1] !== '[') {
    return null;
  }

  i += 2;
  const contentStart = i;
  while (i < text.length) {
    if (isNewline(text[i])) {
      return null;
    }

    if (text[i] === ']' && text[i + 1] === ']') {
      const content = text.slice(contentStart, i);
      const pipe = content.indexOf('|');
      if (pipe === -1) {
        return {end: i + 2, target: content, display: null};
      }

      const target = content.slice(0, pipe);
      const display = content.slice(pipe + 1);
      if (isImage && isEmbedSize(display)) {
        return {end: i + 2, target, display: null};
      }

      return {end: i + 2, target, display};
    }

    i++;
  }

  return null;
}

function parseInlineLink(text: string, openBracket: number): ParsedInline | null {
  const label = parseLabel(text, openBracket + 1);
  if (!label) {
    return null;
  }

  let i = label.end;
  if (i >= text.length || text[i] !== '(') {
    return null;
  }

  i++;
  let hasNewline = label.hasNewline;
  const leading = skipWhitespace(text, i);
  i = leading.index;
  hasNewline = hasNewline || leading.hasNewline;

  let destination = '';
  if (i < text.length && text[i] === '<') {
    const angle = parseAngleDestination(text, i);
    if (!angle) {
      return null;
    }

    destination = unescapeMarkdown(angle.raw, true);
    hasNewline = hasNewline || angle.hasNewline;
    i = angle.end;
  } else if (i < text.length && text[i] !== ')') {
    const bare = parseBareDestination(text, i);
    if (!bare) {
      return null;
    }

    destination = unescapeMarkdown(bare.raw, true);
    hasNewline = hasNewline || bare.hasNewline;
    i = bare.end;
  }

  const middle = skipWhitespace(text, i);
  i = middle.index;
  hasNewline = hasNewline || middle.hasNewline;

  let hasTitle = false;
  if (i < text.length && (text[i] === '"' || text[i] === '\'' || text[i] === '(')) {
    const title = parseTitle(text, i);
    if (!title) {
      return null;
    }

    hasTitle = true;
    hasNewline = hasNewline || title.hasNewline;
    i = title.end;
    const afterTitle = skipWhitespace(text, i);
    i = afterTitle.index;
    hasNewline = hasNewline || afterTitle.hasNewline;
  }

  if (i >= text.length || text[i] !== ')') {
    return null;
  }

  return {
    end: i + 1,
    label: label.label,
    destination,
    hasTitle,
    hasNewline,
  };
}

function parseLabel(text: string, i: number): {label: string, end: number, hasNewline: boolean} | null {
  let depth = 1;
  let label = '';
  let hasNewline = false;
  while (i < text.length) {
    const char = text[i];
    if (char === '\\' && i + 1 < text.length && ASCII_PUNCTUATION.has(text[i + 1])) {
      label += text[i + 1];
      i += 2;
      continue;
    }

    if (isNewline(char)) {
      hasNewline = true;
      label += char;
      i++;
      continue;
    }

    if (char === '[') {
      depth++;
      label += char;
      i++;
      continue;
    }

    if (char === ']') {
      depth--;
      if (depth === 0) {
        return {label, end: i + 1, hasNewline};
      }

      label += char;
      i++;
      continue;
    }

    label += char;
    i++;
  }

  return null;
}

function parseBareDestination(text: string, i: number): {raw: string, end: number, hasNewline: boolean} | null {
  const start = i;
  let depth = 0;
  let hasNewline = false;
  while (i < text.length) {
    const char = text[i];
    if (char === '\\' && i + 1 < text.length) {
      if (isNewline(text[i + 1])) {
        hasNewline = true;
      }

      i += 2;
      continue;
    }

    if (char === ' ' || char === '\t' || isNewline(char)) {
      break;
    }

    const code = char.charCodeAt(0);
    if (code < 32 || code === 127) {
      break;
    }

    if (char === '(') {
      depth++;
      i++;
      continue;
    }

    if (char === ')') {
      if (depth === 0) {
        break;
      }

      depth--;
      i++;
      continue;
    }

    i++;
  }

  if (i === start || depth !== 0) {
    return null;
  }

  return {raw: text.slice(start, i), end: i, hasNewline};
}

function parseAngleDestination(text: string, i: number): {raw: string, end: number, hasNewline: boolean} | null {
  i++;
  const start = i;
  let hasNewline = false;
  while (i < text.length) {
    const char = text[i];
    if (char === '\\' && i + 1 < text.length) {
      if (isNewline(text[i + 1])) {
        hasNewline = true;
      }

      i += 2;
      continue;
    }

    if (isNewline(char)) {
      hasNewline = true;
      i++;
      continue;
    }

    if (char === '<') {
      return null;
    }

    if (char === '>') {
      return {raw: text.slice(start, i), end: i + 1, hasNewline};
    }

    i++;
  }

  return null;
}

function parseTitle(text: string, i: number): {end: number, hasNewline: boolean} | null {
  const opener = text[i];
  const closer = opener === '(' ? ')' : opener;
  if (opener !== '"' && opener !== '\'' && opener !== '(') {
    return null;
  }

  i++;
  let hasNewline = false;
  while (i < text.length) {
    const char = text[i];
    if (char === '\\' && i + 1 < text.length) {
      if (isNewline(text[i + 1])) {
        hasNewline = true;
      }

      i += 2;
      continue;
    }

    if (isNewline(char)) {
      hasNewline = true;
      i++;
      continue;
    }

    if (char === closer) {
      return {end: i + 1, hasNewline};
    }

    i++;
  }

  return null;
}

function skipWhitespace(text: string, i: number): {index: number, hasNewline: boolean} {
  let hasNewline = false;
  while (i < text.length) {
    const char = text[i];
    if (char === ' ' || char === '\t') {
      i++;
      continue;
    }

    if (isNewline(char)) {
      hasNewline = true;
      i++;
      continue;
    }

    break;
  }

  return {index: i, hasNewline};
}

function unescapeMarkdown(raw: string, unescapeSpaces: boolean): string {
  let out = '';
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] === '\\' && i + 1 < raw.length) {
      const next = raw[i + 1];
      if (ASCII_PUNCTUATION.has(next) || (unescapeSpaces && next === ' ')) {
        out += next;
        i++;
        continue;
      }
    }

    out += raw[i];
  }

  return out;
}
