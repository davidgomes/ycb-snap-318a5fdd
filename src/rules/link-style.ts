import {IgnoreType, IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValues = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle?: LinkStyleValues = 'no-change';
  imageStyle?: LinkStyleValues = 'no-change';
}

type WikiLink = {
  content: string,
  end: number,
};

type MarkdownLink = {
  label: string,
  destination: string,
  end: number,
  isConvertible: boolean,
};

// an unclosed `%%` comments out the rest of the file in Obsidian
const obsidianComment: IgnoreType = {replaceAction: /%%[^]*?(?:%%|$)/g, placeholder: '{OBSIDIAN_COMMENT_PLACEHOLDER}'};
const ruleIgnoreTypes: IgnoreType[] = [IgnoreTypes.templaterCommand, IgnoreTypes.yaml, IgnoreTypes.code, IgnoreTypes.inlineCode, IgnoreTypes.math, IgnoreTypes.inlineMath, IgnoreTypes.html, obsidianComment, IgnoreTypes.table];
const ignorePlaceholders = [IgnoreTypes.customIgnore, ...ruleIgnoreTypes].map((ignoreType) => ignoreType.placeholder);

const embedSizeRegex = /^\d+(x\d+)?$/;
const asciiPunctuationRegex = /^[!-/:-@[-`{-~]$/;
const escapedAsciiPunctuationRegex = /\\([!-/:-@[-`{-~])/g;
const uriSchemeRegex = /^[a-z][a-z\d+.-]*:/i;

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
      ruleIgnoreTypes: ruleIgnoreTypes,
    });
  }
  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }
  apply(text: string, options: LinkStyleOptions): string {
    return updateLinkStyles(text, options.linkStyle, options.imageStyle);
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Wiki links are converted to markdown links when `Link Style` is set to `markdown`',
        before: dedent`
          [[Note]]
          [[Note|Display Text]]
          [[Note#Heading]]
          [[#Heading]]
          [[My Note]]
          ![[image.png]]
        `,
        after: dedent`
          [Note](Note)
          [Display Text](Note)
          [Note > Heading](Note#Heading)
          [Heading](#Heading)
          [My Note](<My Note>)
          ![[image.png]]
        `,
        options: {
          linkStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Internal markdown links are converted to wiki links when `Link Style` is set to `wiki`',
        before: dedent`
          [Note](Note)
          [Display Text](Note)
          [Note > Heading](Note#Heading)
          [Display Text](<My Note>)
          [Obsidian](https://obsidian.md)
          [Link with a title](Note "Title")
        `,
        after: dedent`
          [[Note]]
          [[Note|Display Text]]
          [[Note#Heading]]
          [[My Note|Display Text]]
          [Obsidian](https://obsidian.md)
          [Link with a title](Note "Title")
        `,
        options: {
          linkStyle: 'wiki',
        },
      }),
      new ExampleBuilder({
        description: 'Wiki embeds are converted to markdown images when `Image Style` is set to `markdown`',
        before: dedent`
          ![[image.png]]
          ![[image.png|300]]
          ![[image.png|300x200]]
          ![[image.png|Alt text]]
          [[Note]]
        `,
        after: dedent`
          ![image.png](image.png)
          ![image.png](image.png)
          ![image.png](image.png)
          ![Alt text](image.png)
          [[Note]]
        `,
        options: {
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder({
        description: 'Internal markdown images are converted to wiki embeds when `Image Style` is set to `wiki`',
        before: dedent`
          ![](image.png)
          ![image.png](image.png)
          ![Alt text](image.png)
          ![Remote image](https://example.com/image.png)
        `,
        after: dedent`
          ![[image.png]]
          ![[image.png]]
          ![[image.png|Alt text]]
          ![Remote image](https://example.com/image.png)
        `,
        options: {
          imageStyle: 'wiki',
        },
      }),
      new ExampleBuilder({
        description: 'Links in code, math, comments, and tables are left alone',
        before: dedent`
          [[Note]]
          \`[[Note]]\`
          $[[Note]]$
          %% [[Note]] %%
          ${''}
          | Column |
          | ------ |
          | [[Note]] |
        `,
        after: dedent`
          [Note](Note)
          \`[[Note]]\`
          $[[Note]]$
          %% [[Note]] %%
          ${''}
          | Column |
          | ------ |
          | [[Note]] |
        `,
        options: {
          linkStyle: 'markdown',
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
            description: 'Converts internal markdown links to wiki links',
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
            description: 'Converts internal markdown images to wiki embeds',
          },
        ],
      }),
    ];
  }
}

function updateLinkStyles(text: string, linkStyle: LinkStyleValues, imageStyle: LinkStyleValues): string {
  let newText = '';
  let lastCopiedIndex = 0;
  let i = 0;
  while (i < text.length) {
    if (text[i] === '\\') {
      i += 2;
      continue;
    }

    const isEmbed = text[i] === '!' && text[i + 1] === '[';
    const openingBracketIndex = isEmbed ? i + 1 : i;
    if (text[openingBracketIndex] !== '[') {
      i++;
      continue;
    }

    const style = isEmbed ? imageStyle : linkStyle;
    let end = -1;
    let replacement: string | null = null;
    const wikiLink = parseWikiLink(text, openingBracketIndex);
    if (wikiLink) {
      end = wikiLink.end;
      if (style === 'markdown') {
        replacement = convertWikiLinkToMarkdown(wikiLink.content, isEmbed);
      }
    } else {
      const markdownLink = parseMarkdownInlineLink(text, openingBracketIndex);
      if (markdownLink) {
        end = markdownLink.end;
        if (style === 'wiki' && markdownLink.isConvertible) {
          replacement = convertMarkdownLinkToWiki(markdownLink, isEmbed);
        }
      }
    }

    if (end === -1) {
      i = openingBracketIndex + 1;
      continue;
    }

    const originalValue = text.substring(i, end);
    if (replacement !== null && !ignorePlaceholders.some((placeholder) => originalValue.includes(placeholder))) {
      newText += text.substring(lastCopiedIndex, i) + replacement;
      lastCopiedIndex = end;
    }

    i = end;
  }

  return newText + text.substring(lastCopiedIndex);
}

function parseWikiLink(text: string, openingBracketIndex: number): WikiLink | null {
  if (text[openingBracketIndex + 1] !== '[') {
    return null;
  }

  const contentStart = openingBracketIndex + 2;
  for (let i = contentStart; i < text.length; i++) {
    const char = text[i];
    if (char === ']') {
      if (text[i + 1] !== ']') {
        return null;
      }

      return {content: text.substring(contentStart, i), end: i + 2};
    } else if (char === '[' || char === '\n' || char === '\r') {
      return null;
    }
  }

  return null;
}

// Links spanning multiple lines or having a title are still parsed so that their contents are not treated as separate links,
// but they are marked as not convertible.
function parseMarkdownInlineLink(text: string, openingBracketIndex: number): MarkdownLink | null {
  let isSingleLine = true;
  let depth = 0;
  let i = openingBracketIndex + 1;
  for (; i < text.length; i++) {
    const char = text[i];
    if (char === '\\' && isAsciiPunctuation(text[i + 1])) {
      i++;
    } else if (char === '[') {
      depth++;
    } else if (char === ']') {
      if (depth === 0) {
        break;
      }

      depth--;
    } else if (char === '\n') {
      if (isBlankLine(text, i + 1)) {
        return null;
      }

      isSingleLine = false;
    }
  }

  if (i >= text.length || text[i + 1] !== '(') {
    return null;
  }

  const label = text.substring(openingBracketIndex + 1, i);
  let whitespace = skipWhitespace(text, i + 2);
  if (!whitespace) {
    return null;
  }

  i = whitespace.index;
  if (whitespace.hasLineEnding) {
    isSingleLine = false;
  }

  let destination = '';
  if (text[i] === '<') {
    for (i++; i < text.length && text[i] !== '>'; i++) {
      const char = text[i];
      if (char === '<' || char === '\n' || char === '\r') {
        return null;
      }

      if (char === '\\' && isEscapableInDestination(text[i + 1])) {
        i++;
      }

      destination += text[i];
    }

    if (i >= text.length) {
      return null;
    }

    i++;
  } else {
    let parenthesesDepth = 0;
    for (; i < text.length; i++) {
      const char = text[i];
      if (char === '\\' && isEscapableInDestination(text[i + 1])) {
        destination += text[++i];
        continue;
      }

      if (char <= ' ') {
        break;
      } else if (char === '(') {
        parenthesesDepth++;
      } else if (char === ')') {
        if (parenthesesDepth === 0) {
          break;
        }

        parenthesesDepth--;
      }

      destination += char;
    }

    if (parenthesesDepth !== 0) {
      return null;
    }
  }

  whitespace = skipWhitespace(text, i);
  if (!whitespace) {
    return null;
  }

  i = whitespace.index;
  if (whitespace.hasLineEnding) {
    isSingleLine = false;
  }

  let hasTitle = false;
  if (text[i] !== ')') {
    const titleEnd = whitespace.hasWhitespace ? getTitleEnd(text, i) : -1;
    if (titleEnd === -1) {
      return null;
    }

    hasTitle = true;
    whitespace = skipWhitespace(text, titleEnd);
    if (!whitespace || text[whitespace.index] !== ')') {
      return null;
    }

    i = whitespace.index;
  }

  return {
    label: label,
    destination: destination,
    end: i + 1,
    isConvertible: isSingleLine && !hasTitle,
  };
}

function getTitleEnd(text: string, openingIndex: number): number {
  const opening = text[openingIndex];
  if (opening !== '"' && opening !== '\'' && opening !== '(') {
    return -1;
  }

  const closing = opening === '(' ? ')' : opening;
  for (let i = openingIndex + 1; i < text.length; i++) {
    const char = text[i];
    if (char === '\\' && isAsciiPunctuation(text[i + 1])) {
      i++;
    } else if (char === closing) {
      return i + 1;
    } else if (char === '(' && opening === '(') {
      return -1;
    } else if (char === '\n' && isBlankLine(text, i + 1)) {
      return -1;
    }
  }

  return -1;
}

// skips spaces and tabs including up to one line ending and returns null when a blank line is found
function skipWhitespace(text: string, index: number): {index: number, hasWhitespace: boolean, hasLineEnding: boolean} | null {
  const start = index;
  let hasLineEnding = false;
  for (; index < text.length; index++) {
    const char = text[index];
    if (char === '\n') {
      if (hasLineEnding) {
        return null;
      }

      hasLineEnding = true;
    } else if (char !== ' ' && char !== '\t' && char !== '\r') {
      break;
    }
  }

  return {index: index, hasWhitespace: index > start, hasLineEnding: hasLineEnding};
}

function isBlankLine(text: string, lineStart: number): boolean {
  let i = lineStart;
  while (i < text.length && (text[i] === ' ' || text[i] === '\t' || text[i] === '\r')) {
    i++;
  }

  return i >= text.length || text[i] === '\n';
}

function isAsciiPunctuation(char: string | undefined): boolean {
  return char !== undefined && asciiPunctuationRegex.test(char);
}

function isEscapableInDestination(char: string | undefined): boolean {
  return char === ' ' || isAsciiPunctuation(char);
}

function isConvertibleTarget(target: string): boolean {
  if (target === '' || target !== target.trim() || /[[\]|\\\n\r]/.test(target)) {
    return false;
  }

  if (target.includes('://') || uriSchemeRegex.test(target)) {
    return false;
  }

  const headingIndex = target.indexOf('#');
  return headingIndex === -1 || headingIndex < target.length - 1;
}

// mirrors how Obsidian displays a wiki link without display text, i.e. `Page#Heading` is shown as `Page > Heading`
function getDefaultDisplayText(target: string): string {
  return target.split('#').filter((part) => part !== '').join(' > ');
}

function hasBalancedParentheses(value: string): boolean {
  let depth = 0;
  for (const char of value) {
    if (char === '(') {
      depth++;
    } else if (char === ')' && --depth < 0) {
      return false;
    }
  }

  return depth === 0;
}

function formatMarkdownDestination(target: string): string {
  if (!/[\s<>]/.test(target) && hasBalancedParentheses(target)) {
    return target;
  }

  return `<${target.replace(/[<>]/g, '\\$&')}>`;
}

function convertWikiLinkToMarkdown(content: string, isEmbed: boolean): string | null {
  const pipeIndex = content.indexOf('|');
  const target = pipeIndex === -1 ? content : content.substring(0, pipeIndex);
  let displayText = pipeIndex === -1 ? null : content.substring(pipeIndex + 1);
  if (!isConvertibleTarget(target)) {
    return null;
  }

  if (displayText !== null) {
    if (displayText.trim() === '' || displayText.includes('|') || displayText.endsWith('\\')) {
      return null;
    }

    if (isEmbed && embedSizeRegex.test(displayText)) {
      displayText = null;
    }
  }

  const prefix = isEmbed ? '!' : '';
  return `${prefix}[${displayText ?? getDefaultDisplayText(target)}](${formatMarkdownDestination(target)})`;
}

// Obsidian percent-encodes markdown link destinations, e.g. spaces become `%20`
function decodeDestination(destination: string): string {
  try {
    return decodeURIComponent(destination);
  } catch {
    return destination;
  }
}

function containsMarkdownInlineLink(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\\') {
      i++;
    } else if (text[i] === '[' && parseMarkdownInlineLink(text, i)) {
      return true;
    }
  }

  return false;
}

function convertMarkdownLinkToWiki(link: MarkdownLink, isEmbed: boolean): string | null {
  const target = decodeDestination(link.destination);
  if (!isConvertibleTarget(target) || link.label.startsWith('^') || containsMarkdownInlineLink(link.label)) {
    return null;
  }

  const displayText = link.label.replace(escapedAsciiPunctuationRegex, '$1');
  if (displayText.includes('[[') || displayText.includes(']]') || displayText.includes('|')) {
    return null;
  }

  const hasEmptyDisplayText = displayText.trim() === '';
  if (hasEmptyDisplayText && !isEmbed) {
    return null;
  }

  const prefix = isEmbed ? '!' : '';
  if (hasEmptyDisplayText || displayText === target || displayText === getDefaultDisplayText(target)) {
    return `${prefix}[[${target}]]`;
  }

  // a numeric display text on an embed would be treated as its size by Obsidian
  if (isEmbed && embedSizeRegex.test(displayText)) {
    return null;
  }

  return `${prefix}[[${target}|${displayText}]]`;
}
