import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import {IgnoreTypes} from '../utils/ignore-types';

type LinkStyleValue = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValue = 'no-change';
  imageStyle: LinkStyleValue = 'no-change';
}

type ParsedMarkdownLink = {
  end: number;
  label: string;
  target: string;
  image: boolean;
};

const obsidianCommentIgnore = {
  replaceAction: /%%[\s\S]*?%%/g,
  placeholder: '{LINK_STYLE_OBSIDIAN_COMMENT_PLACEHOLDER}',
};

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: [
        IgnoreTypes.code,
        IgnoreTypes.inlineCode,
        IgnoreTypes.math,
        IgnoreTypes.inlineMath,
        IgnoreTypes.yaml,
        IgnoreTypes.html,
        IgnoreTypes.templaterCommand,
        IgnoreTypes.obsidianMultiLineComments,
        IgnoreTypes.table,
        obsidianCommentIgnore,
      ],
    });
  }

  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }

  apply(text: string, options: LinkStyleOptions): string {
    if (options.linkStyle === 'markdown' || options.imageStyle === 'markdown') {
      text = this.convertWikiToMarkdown(
          text,
          options.linkStyle === 'markdown',
          options.imageStyle === 'markdown',
      );
    }
    if (options.linkStyle === 'wiki' || options.imageStyle === 'wiki') {
      text = this.convertMarkdownToWiki(
          text,
          options.linkStyle === 'wiki',
          options.imageStyle === 'wiki',
      );
    }

    return text;
  }

  private convertWikiToMarkdown(text: string, convertLinks: boolean, convertImages: boolean): string {
    let result = '';
    let index = 0;

    while (index < text.length) {
      const isImage = text.startsWith('![[', index);
      const isLink = !isImage && text.startsWith('[[', index);
      if (!isImage && !isLink) {
        result += text[index++];
        continue;
      }

      const end = text.indexOf(']]', index + (isImage ? 3 : 2));
      if (end === -1) {
        result += text[index++];
        continue;
      }

      const original = text.substring(index, end + 2);
      if (original.includes('\n') || original.includes('\r')) {
        result += original;
        index = end + 2;
        continue;
      }

      const shouldConvert = isImage ? convertImages : convertLinks;
      if (!shouldConvert) {
        result += original;
        index = end + 2;
        continue;
      }

      const contents = text.substring(index + (isImage ? 3 : 2), end);
      const separator = contents.indexOf('|');
      const target = separator === -1 ? contents : contents.substring(0, separator);
      const display = separator === -1 ? undefined : contents.substring(separator + 1);
      if (target === '' || target.includes('\r') || target.includes('\n')) {
        result += original;
        index = end + 2;
        continue;
      }

      let linkDisplay = display ?? this.defaultHeadingDisplay(target);
      if (isImage && (display === '300' || display === '300x200')) {
        linkDisplay = target;
      } else if (linkDisplay === '') {
        linkDisplay = target;
      }

      result += isImage ? `![${linkDisplay}](${target})` : `[${linkDisplay}](${target})`;
      index = end + 2;
    }

    return result;
  }

  private convertMarkdownToWiki(text: string, convertLinks: boolean, convertImages: boolean): string {
    let result = '';
    let index = 0;

    while (index < text.length) {
      const isImage = text.startsWith('![', index);
      const isLink = !isImage && text[index] === '[';
      if (!isImage && !isLink) {
        result += text[index++];
        continue;
      }

      const parsed = this.parseMarkdownLink(text, isImage ? index + 1 : index, isImage);
      if (!parsed) {
        result += text[index++];
        continue;
      }

      const shouldConvert = isImage ? convertImages : convertLinks;
      if (!shouldConvert) {
        result += text.substring(index, parsed.end);
        index = parsed.end;
        continue;
      }

      const replacement = this.markdownLinkToWiki(parsed);
      if (replacement === null) {
        result += text.substring(index, parsed.end);
      } else {
        result += replacement;
      }
      index = parsed.end;
    }

    return result;
  }

  private parseMarkdownLink(text: string, openingBracket: number, image: boolean): ParsedMarkdownLink | null {
    let depth = 1;
    let index = openingBracket + 1;

    while (index < text.length) {
      const character = text[index];
      if (character === '\n' || character === '\r') {
        return null;
      }
      if (character === '\\' && index + 1 < text.length) {
        index += 2;
        continue;
      }
      if (character === '[') {
        depth++;
      } else if (character === ']') {
        depth--;
        if (depth === 0) {
          break;
        }
      }
      index++;
    }

    if (depth !== 0 || text[index + 1] !== '(') {
      return null;
    }

    const parenthesisStart = index + 1;
    let parenthesisDepth = 0;
    let end = parenthesisStart + 1;
    while (end < text.length) {
      const character = text[end];
      if (character === '\n' || character === '\r') {
        return null;
      }
      if (character === '\\' && end + 1 < text.length) {
        end += 2;
        continue;
      }
      if (character === '(') {
        parenthesisDepth++;
      } else if (character === ')') {
        if (parenthesisDepth === 0) {
          break;
        }
        parenthesisDepth--;
      }
      end++;
    }

    if (end >= text.length || parenthesisDepth !== 0) {
      return null;
    }

    const destinationArea = text.substring(parenthesisStart + 1, end);
    const destination = this.parseMarkdownDestination(destinationArea);
    if (destination === null) {
      return null;
    }

    return {
      end: end + 1,
      label: text.substring(openingBracket + 1, index),
      target: destination,
      image,
    };
  }

  private parseMarkdownDestination(destinationArea: string): string | null {
    if (destinationArea.includes('\n') || destinationArea.includes('\r')) {
      return null;
    }

    const area = destinationArea.trim();
    if (area === '') {
      return null;
    }

    if (area.startsWith('<')) {
      let closingBracket = -1;
      for (let index = 1; index < area.length; index++) {
        if (area[index] === '\\') {
          index++;
          continue;
        }
        if (area[index] === '>') {
          closingBracket = index;
          break;
        }
      }

      if (closingBracket === -1 || area.substring(closingBracket + 1).trim() !== '') {
        return null;
      }

      return this.unescapeMarkdownDestination(area.substring(1, closingBracket));
    }

    let targetEnd = area.length;
    for (let index = 0; index < area.length; index++) {
      if (area[index] === '\\') {
        index++;
        continue;
      }
      if (/\s/.test(area[index])) {
        targetEnd = index;
        break;
      }
    }

    const rawTarget = area.substring(0, targetEnd);
    if (rawTarget === '' || area.substring(targetEnd).trim() !== '') {
      return null;
    }

    return this.unescapeMarkdownDestination(rawTarget);
  }

  private unescapeMarkdownDestination(target: string): string {
    let result = '';
    for (let index = 0; index < target.length; index++) {
      if (target[index] === '\\' && index + 1 < target.length && (/\s|[!"#$%&'()*+,\-./:;<=>?@[\\\]^_`{|}~]/).test(target[index + 1])) {
        result += target[++index];
      } else {
        result += target[index];
      }
    }
    return result;
  }

  private markdownLinkToWiki(link: ParsedMarkdownLink): string | null {
    if (link.target.includes('://')) {
      return null;
    }

    if (link.image) {
      if (link.label === '' || link.label === link.target) {
        return `![[${link.target}]]`;
      }
      return `![[${link.target}|${link.label}]]`;
    }

    if (link.label === link.target || link.label === this.defaultHeadingDisplay(link.target)) {
      return `[[${link.target}]]`;
    }
    return `[[${link.target}|${link.label}]]`;
  }

  private defaultHeadingDisplay(target: string): string {
    const headingSeparator = target.indexOf('#');
    if (headingSeparator === -1) {
      return target;
    }
    if (headingSeparator === 0) {
      return target.substring(1);
    }
    return `${target.substring(0, headingSeparator)} > ${target.substring(headingSeparator + 1)}`;
  }

  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder<LinkStyleOptions>({
        description: 'Wiki links and embeds are converted to markdown when both styles are set to `markdown`',
        before: '[[note]]\n![[image.png]]',
        after: '[note](note)\n![image.png](image.png)',
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
    ];
  }

  get optionBuilders(): OptionBuilderBase<LinkStyleOptions>[] {
    return [
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.link-style.name',
        descriptionKey: 'rules.link-style.link-style.description',
        optionsKey: 'linkStyle',
        records: [
          {value: 'no-change', description: 'Leave links unchanged'},
          {value: 'markdown', description: 'Use markdown links'},
          {value: 'wiki', description: 'Use Obsidian wiki links'},
        ],
      }),
      new DropdownOptionBuilder<LinkStyleOptions, LinkStyleValue>({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.image-style.name',
        descriptionKey: 'rules.link-style.image-style.description',
        optionsKey: 'imageStyle',
        records: [
          {value: 'no-change', description: 'Leave images unchanged'},
          {value: 'markdown', description: 'Use markdown images'},
          {value: 'wiki', description: 'Use Obsidian wiki embeds'},
        ],
      }),
    ];
  }
}
