import {IgnoreType, IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValues = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValues = 'no-change';
  imageStyle: LinkStyleValues = 'no-change';
}

const obsidianCommentIgnoreType: IgnoreType = {replaceAction: /%%[^]*?%%/g, placeholder: '{LINK_STYLE_OBSIDIAN_COMMENT_PLACEHOLDER}'};

const wikiLinkOrEmbedRegex = /(!?)\[\[([^\][\n|]+)(?:\|([^\][\n]*))?\]\]/g;
const stickyWikiLinkRegex = /\[\[[^\][\n|]+(?:\|[^\][\n]*)?\]\]/y;
const embedSizeRegex = /^\d+(x\d+)?$/;
const asciiPunctuationRegex = /[!-/:-@[-`{-~]/;

type ParsedMarkdownLink = {
  label: string,
  rawLabel: string,
  destination: string,
  end: number,
};

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [IgnoreTypes.yaml, IgnoreTypes.templaterCommand, IgnoreTypes.code, IgnoreTypes.inlineCode, obsidianCommentIgnoreType, IgnoreTypes.math, IgnoreTypes.inlineMath, IgnoreTypes.html, IgnoreTypes.table],
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    const linksToMarkdown = options.linkStyle === 'markdown';
    const imagesToMarkdown = options.imageStyle === 'markdown';
    if (linksToMarkdown || imagesToMarkdown) {
      text = convertWikiToMarkdown(text, linksToMarkdown, imagesToMarkdown);
    }

    const linksToWiki = options.linkStyle === 'wiki';
    const imagesToWiki = options.imageStyle === 'wiki';
    if (linksToWiki || imagesToWiki) {
      text = convertMarkdownToWiki(text, linksToWiki, imagesToWiki);
    }

    return text;
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Converting wiki links and embeds to markdown',
        before: dedent`
          [[Page]]
          [[Page|Display]]
          [[Page#Heading]]
          [[#Heading]]
          ![[image.png]]
          ![[image.png|300x200]]
        `,
        after: dedent`
          [Page](Page)
          [Display](Page)
          [Page > Heading](Page#Heading)
          [Heading](#Heading)
          ![image.png](image.png)
          ![image.png](image.png)
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Converting markdown links and images to wiki',
        before: dedent`
          [Page](Page)
          [Display](Page)
          [Page > Heading](Page#Heading)
          [My Page](<My Page>)
          ![image.png](image.png)
          ![alt text](image.png)
          [External](https://example.com)
          [Titled](Page "title")
        `,
        after: dedent`
          [[Page]]
          [[Page|Display]]
          [[Page#Heading]]
          [[My Page]]
          ![[image.png]]
          ![[image.png|alt text]]
          [External](https://example.com)
          [Titled](Page "title")
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

function getDefaultDisplay(target: string): string {
  return target.split('#').filter((part) => part !== '').join(' > ');
}

function hasBalancedParentheses(value: string): boolean {
  let depth = 0;
  for (const char of value) {
    if (char === '(') {
      depth++;
    } else if (char === ')') {
      if (depth === 0) {
        return false;
      }
      depth--;
    }
  }

  return depth === 0;
}

function toMarkdownDestination(target: string): string {
  if (/[\s<>]/.test(target) || !hasBalancedParentheses(target)) {
    return '<' + target.replace(/[<>]/g, '\\$&') + '>';
  }

  return target;
}

function convertWikiToMarkdown(text: string, convertLinks: boolean, convertEmbeds: boolean): string {
  return text.replace(wikiLinkOrEmbedRegex, (match: string, bang: string, target: string, display: string | undefined, offset: number) => {
    if (offset > 0 && text[offset - 1] === '\\') {
      return match;
    }

    const isEmbed = bang === '!';
    if ((isEmbed && !convertEmbeds) || (!isEmbed && !convertLinks)) {
      return match;
    }

    if (isEmbed && display != undefined && embedSizeRegex.test(display)) {
      display = undefined;
    }

    const label = display != undefined && display !== '' ? display : getDefaultDisplay(target);

    return `${bang}[${label}](${toMarkdownDestination(target)})`;
  });
}

function isEscapable(char: string): boolean {
  return char != undefined && (char === ' ' || asciiPunctuationRegex.test(char));
}

function skipSpacesAndTabs(text: string, index: number): number {
  while (index < text.length && (text[index] === ' ' || text[index] === '\t')) {
    index++;
  }

  return index;
}

function parseInlineMarkdownLink(text: string, start: number): ParsedMarkdownLink | null {
  let index = start + 1;
  let depth = 0;
  let label = '';
  for (; index < text.length; index++) {
    const char = text[index];
    if (char === '\n') {
      return null;
    }

    if (char === '\\' && asciiPunctuationRegex.test(text[index + 1] ?? '')) {
      label += text[index + 1];
      index++;
      continue;
    }

    if (char === '[') {
      depth++;
    } else if (char === ']') {
      if (depth === 0) {
        break;
      }
      depth--;
    }

    label += char;
  }

  if (index >= text.length) {
    return null;
  }

  const rawLabel = text.substring(start + 1, index);
  index++;
  if (text[index] !== '(') {
    return null;
  }

  index = skipSpacesAndTabs(text, index + 1);

  let destination = '';
  if (text[index] === '<') {
    index++;
    for (;;) {
      if (index >= text.length) {
        return null;
      }

      const char = text[index];
      if (char === '\n' || char === '<') {
        return null;
      }

      if (char === '\\' && isEscapable(text[index + 1])) {
        destination += text[index + 1];
        index += 2;
        continue;
      }

      index++;
      if (char === '>') {
        break;
      }

      destination += char;
    }
  } else {
    let parenDepth = 0;
    while (index < text.length) {
      const char = text[index];
      if (char === '\\' && isEscapable(text[index + 1])) {
        destination += text[index + 1];
        index += 2;
        continue;
      }

      if (char <= ' ') {
        break;
      }

      if (char === '(') {
        parenDepth++;
      } else if (char === ')') {
        if (parenDepth === 0) {
          break;
        }
        parenDepth--;
      }

      destination += char;
      index++;
    }

    if (parenDepth !== 0) {
      return null;
    }
  }

  index = skipSpacesAndTabs(text, index);
  if (text[index] !== ')') {
    return null;
  }

  return {label, rawLabel, destination, end: index + 1};
}

function toWikiLink(link: ParsedMarkdownLink, isImage: boolean): string | null {
  const {label, rawLabel, destination} = link;
  if (destination === '' || destination.includes('://') || /[[\]|\n]/.test(destination)) {
    return null;
  }

  if (/\[\[|\]\]|\||\n/.test(label) || /(^|[^\\])\]\(/.test(rawLabel)) {
    return null;
  }

  const omitDisplay = label === '' || label === destination || label === getDefaultDisplay(destination);
  const wikiLink = omitDisplay ? `[[${destination}]]` : `[[${destination}|${label}]]`;

  return isImage ? '!' + wikiLink : wikiLink;
}

function convertMarkdownToWiki(text: string, convertLinks: boolean, convertImages: boolean): string {
  let result = '';
  let index = 0;
  while (index < text.length) {
    const char = text[index];
    if (char === '\\') {
      result += text.substring(index, index + 2);
      index += 2;
      continue;
    }

    const isImage = char === '!' && text[index + 1] === '[';
    if (char === '[' || isImage) {
      const bracketStart = isImage ? index + 1 : index;

      stickyWikiLinkRegex.lastIndex = bracketStart;
      const wikiMatch = stickyWikiLinkRegex.exec(text);
      if (wikiMatch) {
        const end = bracketStart + wikiMatch[0].length;
        result += text.substring(index, end);
        index = end;
        continue;
      }

      if (isImage ? convertImages : convertLinks) {
        const parsedLink = parseInlineMarkdownLink(text, bracketStart);
        const wikiLink = parsedLink ? toWikiLink(parsedLink, isImage) : null;
        if (wikiLink) {
          result += wikiLink;
          index = parsedLink.end;
          continue;
        }
      }

      result += text.substring(index, bracketStart + 1);
      index = bracketStart + 1;
      continue;
    }

    result += char;
    index++;
  }

  return result;
}
