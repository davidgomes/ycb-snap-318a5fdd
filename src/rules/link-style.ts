import {IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValues = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle?: LinkStyleValues = 'no-change';
  imageStyle?: LinkStyleValues = 'no-change';
}

const wikiRegex = /(!?)\[\[([^[\]\n|]+)(?:\|([^[\]\n]*))?\]\]/g;
const embedSizeRegex = /^\d+(x\d+)?$/;

function defaultDisplay(target: string): string {
  if (!target.includes('#')) {
    return target;
  }

  return target.split('#').filter((part) => part !== '').join(' > ');
}

function formatMarkdownDestination(target: string): string {
  if (/[\s<>]/.test(target) || !hasBalancedParens(target)) {
    return '<' + target.replace(/([<>\\])/g, '\\$1') + '>';
  }

  return target;
}

function hasBalancedParens(text: string): boolean {
  let depth = 0;
  for (const char of text) {
    if (char === '(') {
      depth++;
    } else if (char === ')') {
      depth--;
      if (depth < 0) {
        return false;
      }
    }
  }

  return depth === 0;
}

function convertWikiToMarkdown(text: string, convertLinks: boolean, convertImages: boolean): string {
  return text.replace(wikiRegex, (match: string, bang: string, rawTarget: string, rawDisplay?: string) => {
    const isEmbed = bang === '!';
    if ((isEmbed && !convertImages) || (!isEmbed && !convertLinks)) {
      return match;
    }

    const target = rawTarget.trim();
    let display = rawDisplay?.trim();
    if (isEmbed && display != undefined && embedSizeRegex.test(display)) {
      display = undefined;
    }

    if (display == undefined || display === '') {
      display = defaultDisplay(target);
    }

    const escapedDisplay = display.replace(/([[\]\\])/g, '\\$1');
    return `${bang}[${escapedDisplay}](${formatMarkdownDestination(target)})`;
  });
}

type ParsedMarkdownLink = {end: number, label: string, target: string};

function parseMarkdownLink(text: string, openBracket: number): ParsedMarkdownLink | null {
  let i = openBracket + 1;
  let depth = 1;
  let label = '';
  while (i < text.length) {
    const char = text[i];
    if (char === '\n') {
      return null;
    }

    if (char === '\\' && i + 1 < text.length && text[i + 1] !== '\n') {
      label += text[i + 1];
      i += 2;
      continue;
    }

    if (char === '[') {
      depth++;
    } else if (char === ']') {
      depth--;
      if (depth === 0) {
        break;
      }
    }

    label += char;
    i++;
  }

  if (depth !== 0 || text[i + 1] !== '(') {
    return null;
  }

  i += 2;
  const skipSpaces = () => {
    while (i < text.length && (text[i] === ' ' || text[i] === '\t')) {
      i++;
    }
  };

  skipSpaces();
  let target = '';
  if (text[i] === '<') {
    i++;
    let closed = false;
    while (i < text.length) {
      const char = text[i];
      if (char === '\n' || char === '<') {
        return null;
      }

      if (char === '\\' && i + 1 < text.length && text[i + 1] !== '\n') {
        target += text[i + 1];
        i += 2;
        continue;
      }

      if (char === '>') {
        closed = true;
        i++;
        break;
      }

      target += char;
      i++;
    }

    if (!closed) {
      return null;
    }
  } else {
    let parenDepth = 0;
    while (i < text.length) {
      const char = text[i];
      if (char === '\n') {
        return null;
      }

      if (char === '\\' && i + 1 < text.length && text[i + 1] !== '\n') {
        target += text[i + 1];
        i += 2;
        continue;
      }

      if (char === ' ' || char === '\t') {
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

      target += char;
      i++;
    }

    if (parenDepth !== 0) {
      return null;
    }
  }

  skipSpaces();
  if (text[i] !== ')') {
    return null;
  }

  return {end: i + 1, label, target};
}

function isEscaped(text: string, index: number): boolean {
  let backslashes = 0;
  for (let j = index - 1; j >= 0 && text[j] === '\\'; j--) {
    backslashes++;
  }

  return backslashes % 2 === 1;
}

function convertMarkdownToWiki(text: string, convertLinks: boolean, convertImages: boolean): string {
  let result = '';
  let i = 0;
  while (i < text.length) {
    if (text[i] !== '[' || isEscaped(text, i) || text[i + 1] === '[' || (i > 0 && text[i - 1] === '[')) {
      result += text[i];
      i++;
      continue;
    }

    const isImage = i > 0 && text[i - 1] === '!' && !isEscaped(text, i - 1);
    const shouldConvert = isImage ? convertImages : convertLinks;
    const parsed = shouldConvert ? parseMarkdownLink(text, i) : null;
    const replacement = parsed ? buildWikiLink(parsed, isImage) : null;
    if (parsed == null || replacement == null) {
      result += text[i];
      i++;
      continue;
    }

    if (isImage) {
      result = result.slice(0, -1);
    }

    result += replacement;
    i = parsed.end;
  }

  return result;
}

function buildWikiLink(link: ParsedMarkdownLink, isImage: boolean): string | null {
  const target = link.target.trim();
  const display = link.label.trim();
  if (target === '' || target.includes('://') || /[[\]|\n]/.test(target) || /\]\]|\n/.test(display)) {
    return null;
  }

  if (!isImage && display === '') {
    return null;
  }

  const omitDisplay = display === '' || display === target || display === defaultDisplay(target);
  const displayPart = omitDisplay ? '' : '|' + display;

  return `${isImage ? '!' : ''}[[${target}${displayPart}]]`;
}

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [IgnoreTypes.yaml, IgnoreTypes.code, IgnoreTypes.inlineCode, IgnoreTypes.math, IgnoreTypes.inlineMath, IgnoreTypes.html, IgnoreTypes.templaterCommand, IgnoreTypes.obsidianMultiLineComments, IgnoreTypes.table],
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    const linkStyle = options.linkStyle ?? 'no-change';
    const imageStyle = options.imageStyle ?? 'no-change';

    const toMarkdownLinks = linkStyle === 'markdown';
    const toMarkdownImages = imageStyle === 'markdown';
    if (toMarkdownLinks || toMarkdownImages) {
      text = convertWikiToMarkdown(text, toMarkdownLinks, toMarkdownImages);
    }

    const toWikiLinks = linkStyle === 'wiki';
    const toWikiImages = imageStyle === 'wiki';
    if (toWikiLinks || toWikiImages) {
      text = convertMarkdownToWiki(text, toWikiLinks, toWikiImages);
    }

    return text;
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Converts wiki links and embeds to markdown when both styles are set to `markdown`',
        before: dedent`
          [[Page]]
          [[Page|Display]]
          [[Page#Heading]]
          ![[image.png|300]]
        `,
        after: dedent`
          [Page](Page)
          [Display](Page)
          [Page > Heading](Page#Heading)
          ![image.png](image.png)
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Converts markdown links and images to wiki when both styles are set to `wiki`',
        before: dedent`
          [Page](Page)
          [Display](Page)
          [Page > Heading](Page#Heading)
          ![alt text](image.png)
          [External](https://example.com)
        `,
        after: dedent`
          [[Page]]
          [[Page|Display]]
          [[Page#Heading]]
          ![[image.png|alt text]]
          [External](https://example.com)
        `,
        options: {
          linkStyle: 'wiki',
          imageStyle: 'wiki',
        },
      }),
    ];
  }
  get optionBuilders(): OptionBuilderBase<LinkStyleOptions>[] {
    const records: {value: LinkStyleValues, description: string}[] = [
      {value: 'no-change', description: 'Leaves the style as is'},
      {value: 'markdown', description: 'Converts to markdown syntax'},
      {value: 'wiki', description: 'Converts to wiki syntax'},
    ];

    return [
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValues>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.link-style.name',
        descriptionKey: 'rules.link-style.link-style.description',
        optionsKey: 'linkStyle',
        records,
      }),
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValues>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.image-style.name',
        descriptionKey: 'rules.link-style.image-style.description',
        optionsKey: 'imageStyle',
        records,
      }),
    ];
  }
}
