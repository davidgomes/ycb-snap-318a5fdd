import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';
import {IgnoreType, IgnoreTypes} from '../utils/ignore-types';

type LinkStyleValues = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValues = 'no-change';
  imageStyle: LinkStyleValues = 'no-change';
}

const obsidianComments: IgnoreType = {replaceAction: /%%[^]*?%%/g, placeholder: '{OBSIDIAN_COMMENT_PLACEHOLDER}'};

const wikiLinkOrEmbedRegex = /(!?)\[\[([^[\]|\n]+)(?:\|((?:[^[\]|\n]|\[(?!\[)|\](?!\]))*))?\]\]/g;
const embedSizeRegex = /^\d+(x\d+)?$/;
const placeholderRegex = /\{[A-Z_]+_PLACEHOLDER\}/i;
const asciiPunctuationRegex = /[!-/:-@[-`{-~]/;

type ParsedMarkdownLink = {
  end: number,
  rawLabel: string,
  target: string,
  hasTitle: boolean,
};

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [IgnoreTypes.yaml, IgnoreTypes.code, IgnoreTypes.inlineCode, IgnoreTypes.templaterCommand, obsidianComments, IgnoreTypes.math, IgnoreTypes.inlineMath, IgnoreTypes.html, IgnoreTypes.table],
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    const linksToMarkdown = options.linkStyle === 'markdown';
    const imagesToMarkdown = options.imageStyle === 'markdown';
    const linksToWiki = options.linkStyle === 'wiki';
    const imagesToWiki = options.imageStyle === 'wiki';

    if (linksToMarkdown || imagesToMarkdown) {
      text = convertWikiToMarkdown(text, linksToMarkdown, imagesToMarkdown);
    }

    if (linksToWiki || imagesToWiki) {
      text = convertMarkdownToWiki(text, linksToWiki, imagesToWiki);
    }

    return text;
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Converting wiki links and embeds to markdown links and images',
        before: dedent`
          [[Page]]
          [[Page|Display Text]]
          [[Page#Heading]]
          [[#Heading]]
          [[My Page]]
          ![[image.png]]
          ![[image.png|300]]
          ![[image.png|Alt Text]]
          \`[[Code]]\`
        `,
        after: dedent`
          [Page](Page)
          [Display Text](Page)
          [Page > Heading](Page#Heading)
          [Heading](#Heading)
          [My Page](<My Page>)
          ![image.png](image.png)
          ![image.png](image.png)
          ![Alt Text](image.png)
          \`[[Code]]\`
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Converting markdown links and images to wiki links and embeds',
        before: dedent`
          [Page](Page)
          [Display Text](Page)
          [Page > Heading](Page#Heading)
          [My Page](<My Page>)
          ![image.png](image.png)
          ![Alt Text](image.png)
          [External](https://example.com)
          [Titled](Page "Title")
          \`[Code](Code)\`
        `,
        after: dedent`
          [[Page]]
          [[Page|Display Text]]
          [[Page#Heading]]
          [[My Page]]
          ![[image.png]]
          ![[image.png|Alt Text]]
          [External](https://example.com)
          [Titled](Page "Title")
          \`[Code](Code)\`
        `,
        options: {
          linkStyle: 'wiki',
          imageStyle: 'wiki',
        },
      }),
      new ExampleBuilder({
        description: 'Links and images can be converted independently of each other',
        before: dedent`
          [[Page]] and ![[image.png]]
        `,
        after: dedent`
          [Page](Page) and ![[image.png]]
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'no-change',
        },
      }),
    ];
  }
  get optionBuilders(): OptionBuilderBase<LinkStyleOptions>[] {
    return [
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValues>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.link-style.name',
        descriptionKey: 'rules.link-style.link-style.description',
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
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValues>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.image-style.name',
        descriptionKey: 'rules.link-style.image-style.description',
        optionsKey: 'imageStyle',
        records: [
          {
            value: 'no-change',
            description: 'Leaves images and embeds as they are',
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

function convertWikiToMarkdown(text: string, convertLinks: boolean, convertImages: boolean): string {
  return text.replace(wikiLinkOrEmbedRegex, (match: string, bang: string, target: string, display: string | undefined, offset: number) => {
    const isEmbed = bang === '!';
    if ((isEmbed && !convertImages) || (!isEmbed && !convertLinks) || isEscaped(text, offset)) {
      return match;
    }

    if (display === '' || placeholderRegex.test(target) || (display != undefined && placeholderRegex.test(display))) {
      return match;
    }

    let label = display;
    if (label == undefined || (isEmbed && embedSizeRegex.test(label))) {
      label = getDefaultDisplay(target);
    }

    return `${bang}[${escapeMarkdownLabel(label)}](${formatMarkdownDestination(target)})`;
  });
}

function convertMarkdownToWiki(text: string, convertLinks: boolean, convertImages: boolean): string {
  let result = '';
  let lastCopiedIndex = 0;
  let i = 0;
  while (i < text.length) {
    const char = text[i];
    if (char === '\\') {
      i += 2;
      continue;
    }

    if (char !== '[') {
      i++;
      continue;
    }

    const parsedLink = parseInlineMarkdownLink(text, i);
    if (parsedLink == null) {
      i++;
      continue;
    }

    const hasBang = i > 0 && text[i - 1] === '!';
    const isImage = hasBang && !isEscaped(text, i - 1);
    const start = isImage ? i - 1 : i;
    const shouldConvert = isImage ? convertImages : convertLinks && !hasBang;
    const replacement = shouldConvert ? buildWikiLink(parsedLink, isImage) : null;
    if (replacement != null) {
      result += text.substring(lastCopiedIndex, start) + replacement;
      lastCopiedIndex = parsedLink.end;
    }

    i = parsedLink.end;
  }

  return result + text.substring(lastCopiedIndex);
}

function buildWikiLink(link: ParsedMarkdownLink, isImage: boolean): string | null {
  const {rawLabel, target} = link;
  if (link.hasTitle || target === '' || target.includes('://') || /[[\]|]/.test(target)) {
    return null;
  }

  // nested inline links or images inside of the label are left alone
  if (placeholderRegex.test(rawLabel) || placeholderRegex.test(target) || /(^|[^\\])\]\(/.test(rawLabel)) {
    return null;
  }

  const label = unescapeMarkdown(rawLabel);
  if (label.includes('|') || label.includes('[[') || label.includes(']]') || label.endsWith(']')) {
    return null;
  }

  if (!isImage && (label === '' || rawLabel.startsWith('^'))) {
    return null;
  }

  const omitDisplay = label === '' || label === target || label === getDefaultDisplay(target);
  const display = omitDisplay ? '' : '|' + label;

  return `${isImage ? '!' : ''}[[${target}${display}]]`;
}

/**
 * Parses a single-line inline markdown link starting at the opening square bracket of its label.
 * @param {string} text The text to parse
 * @param {number} start The index of the opening square bracket of the label
 * @return {ParsedMarkdownLink | null} The parsed link or null when there is no single-line inline link at the index
 */
function parseInlineMarkdownLink(text: string, start: number): ParsedMarkdownLink | null {
  let i = start + 1;
  let depth = 1;
  while (i < text.length) {
    const char = text[i];
    if (char === '\n') {
      return null;
    } else if (char === '\\') {
      if (text[i + 1] === '\n') {
        return null;
      }

      i += 2;
      continue;
    } else if (char === '[') {
      depth++;
    } else if (char === ']') {
      depth--;
      if (depth === 0) {
        break;
      }
    }

    i++;
  }

  if (depth !== 0 || text[i + 1] !== '(') {
    return null;
  }

  const rawLabel = text.substring(start + 1, i);
  i = skipSpacesAndTabs(text, i + 2);

  let target = '';
  if (text[i] === '<') {
    i++;
    while (i < text.length && text[i] !== '>') {
      const char = text[i];
      if (char === '\n' || char === '<') {
        return null;
      }

      if (char === '\\' && isEscapableInDestination(text[i + 1])) {
        target += text[i + 1];
        i += 2;
        continue;
      }

      target += char;
      i++;
    }

    if (text[i] !== '>') {
      return null;
    }

    i++;
  } else {
    let parenDepth = 0;
    while (i < text.length) {
      const char = text[i];
      if (char === ' ' || char === '\t' || char === '\n') {
        break;
      }

      if (char === '\\' && isEscapableInDestination(text[i + 1])) {
        target += text[i + 1];
        i += 2;
        continue;
      }

      if (char === '(') {
        parenDepth++;
      } else if (char === ')') {
        if (parenDepth === 0) {
          break;
        }

        parenDepth--;
      }

      target += char;
      i++;
    }

    if (parenDepth !== 0) {
      return null;
    }
  }

  const afterDestination = i;
  i = skipSpacesAndTabs(text, i);
  if (text[i] === ')') {
    return {end: i + 1, rawLabel, target, hasTitle: false};
  }

  if (i === afterDestination || !['"', '\'', '('].includes(text[i])) {
    return null;
  }

  const titleEnd = findTitleEnd(text, i);
  if (titleEnd === -1) {
    return null;
  }

  return {end: titleEnd, rawLabel, target, hasTitle: true};
}

/**
 * Finds the end of a link title and the closing parenthesis of the link that follows it.
 * @param {string} text The text to search
 * @param {number} start The index of the opening delimiter of the title
 * @return {number} The index after the closing parenthesis of the link or -1 if it is not a valid single-line title
 */
function findTitleEnd(text: string, start: number): number {
  const closingDelimiter = text[start] === '(' ? ')' : text[start];
  let i = start + 1;
  while (i < text.length && text[i] !== closingDelimiter) {
    if (text[i] === '\n' || (closingDelimiter === ')' && text[i] === '(')) {
      return -1;
    }

    i += text[i] === '\\' ? 2 : 1;
  }

  if (i >= text.length) {
    return -1;
  }

  i = skipSpacesAndTabs(text, i + 1);

  return text[i] === ')' ? i + 1 : -1;
}

function skipSpacesAndTabs(text: string, index: number): number {
  while (text[index] === ' ' || text[index] === '\t') {
    index++;
  }

  return index;
}

function isEscapableInDestination(char: string | undefined): boolean {
  return char != undefined && (char === ' ' || asciiPunctuationRegex.test(char));
}

function isEscaped(text: string, index: number): boolean {
  let backslashCount = 0;
  while (index - backslashCount - 1 >= 0 && text[index - backslashCount - 1] === '\\') {
    backslashCount++;
  }

  return backslashCount % 2 === 1;
}

function unescapeMarkdown(text: string): string {
  return text.replace(/\\([!-/:-@[-`{-~])/g, '$1');
}

/**
 * Gets the text Obsidian displays for a wiki link without display text.
 * @param {string} target The wiki link target
 * @return {string} The default display text
 */
function getDefaultDisplay(target: string): string {
  if (!target.includes('#')) {
    return target;
  }

  const [page, ...subpaths] = target.split('#');
  const parts = page === '' ? subpaths : [page, ...subpaths];

  return parts.join(' > ');
}

function escapeBackslashes(text: string, isEscapable: (char: string | undefined) => boolean): string {
  let result = '';
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    const nextChar = text[i + 1];
    if (char === '\\' && (nextChar == undefined || isEscapable(nextChar))) {
      result += '\\\\';
    } else {
      result += char;
    }
  }

  return result;
}

function escapeMarkdownLabel(label: string): string {
  label = escapeBackslashes(label, (char) => asciiPunctuationRegex.test(char));

  return hasBalancedDelimiters(label, '[', ']') ? label : label.replace(/[[\]]/g, '\\$&');
}

function formatMarkdownDestination(target: string): string {
  const escapedTarget = escapeBackslashes(target, isEscapableInDestination);
  if (!/[\s<>]/.test(target) && hasBalancedDelimiters(target, '(', ')')) {
    return escapedTarget;
  }

  return '<' + escapedTarget.replace(/[<>]/g, '\\$&') + '>';
}

function hasBalancedDelimiters(text: string, open: string, close: string): boolean {
  let depth = 0;
  for (const char of text) {
    if (char === open) {
      depth++;
    } else if (char === close) {
      depth--;
      if (depth < 0) {
        return false;
      }
    }
  }

  return depth === 0;
}
