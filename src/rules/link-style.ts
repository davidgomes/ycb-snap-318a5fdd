import {IgnoreTypes} from '../utils/ignore-types';
import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValues = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValues = 'no-change';
  imageStyle: LinkStyleValues = 'no-change';
}

const EMBED_SIZE = /^(?:\d+|\d+x\d+)$/;

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
    let i = 0;
    while (i < text.length) {
      const commentEnd = skippedObsidianCommentEnd(text, i);
      if (commentEnd !== null) {
        result += text.slice(i, commentEnd);
        i = commentEnd;
        continue;
      }

      if (text[i] === '!' && !isEscaped(text, i) && text[i + 1] === '[') {
        const converted = convertAt(text, i + 1, options);
        if (converted) {
          result += converted.value;
          i = converted.end;
          continue;
        }
      }

      if (text[i] === '[') {
        const converted = convertAt(text, i, options);
        if (converted) {
          result += converted.value;
          i = converted.end;
          continue;
        }
      }

      result += text[i];
      i++;
    }

    return result;
  }
  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder<LinkStyleOptions>({
        description: 'Wiki links and embeds become markdown links and images when both styles are markdown',
        before: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          ![[f.png]]
          ![[f.png|300]]
          ![[f.png|300x200]]
        `,
        after: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          ![f.png](f.png)
          ![f.png](f.png)
          ![f.png](f.png)
        `,
        options: {
          linkStyle: 'markdown',
          imageStyle: 'markdown',
        },
      }),
      new ExampleBuilder<LinkStyleOptions>({
        description: 'Inline markdown links and images become wiki links and embeds when both styles are wiki',
        before: dedent`
          [t](t)
          [d](t)
          [p > h](p#h)
          [h](#h)
          ![f.png](f.png)
          ![alt](f.png)
          ![](f.png)
          [site](https://example.com)
        `,
        after: dedent`
          [[t]]
          [[t|d]]
          [[p#h]]
          [[#h]]
          ![[f.png]]
          ![[f.png|alt]]
          ![[f.png]]
          [site](https://example.com)
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

function skippedObsidianCommentEnd(text: string, index: number): number | null {
  if (text.startsWith('%%', index)) {
    const close = text.indexOf('%%', index + 2);
    if (close !== -1) {
      return close + 2;
    }
  }

  return null;
}

function convertAt(text: string, index: number, options: LinkStyleOptions): {value: string, end: number} | null {
  if (isEscaped(text, index)) {
    return null;
  }

  const embed = index > 0 && text[index - 1] === '!' && !isEscaped(text, index - 1);
  if (text.startsWith('[[', index)) {
    return convertWiki(text, index, embed, options);
  }

  if (text[index] !== '[') {
    return null;
  }

  return convertMarkdown(text, index, embed, options);
}

function convertWiki(text: string, index: number, embed: boolean, options: LinkStyleOptions): {value: string, end: number} | null {
  const style = embed ? options.imageStyle : options.linkStyle;
  if (style !== 'markdown') {
    return null;
  }

  const close = text.indexOf(']]', index + 2);
  if (close === -1) {
    return null;
  }

  const inner = text.slice(index + 2, close);
  if (inner.includes('\n') || inner.includes('[') || inner.includes(']')) {
    return null;
  }

  const parts = inner.split('|');
  if (parts.length === 0 || parts.length > 3 || (parts.length === 3 && !embed)) {
    return null;
  }

  const target = parts[0];
  if (target.length === 0) {
    return null;
  }

  let display = parts.length > 1 ? parts[1] : undefined;
  if (parts.length === 3) {
    if (!EMBED_SIZE.test(parts[2])) {
      return null;
    }
  } else if (embed && display !== undefined && EMBED_SIZE.test(display)) {
    display = undefined;
  }

  const label = display !== undefined && display.length > 0 ? display : defaultDisplay(target, embed);
  return {
    value: `${embed ? '!' : ''}[${label}](${target})`,
    end: close + 2,
  };
}

function convertMarkdown(text: string, index: number, embed: boolean, options: LinkStyleOptions): {value: string, end: number} | null {
  const style = embed ? options.imageStyle : options.linkStyle;
  if (style !== 'wiki') {
    return null;
  }

  const label = parseLabel(text, index);
  if (!label) {
    return null;
  }

  if (text[label.end + 1] !== '(') {
    return null;
  }

  const destination = parseDestination(text, label.end + 1);
  if (!destination || destination.hasTitle || destination.target.includes('://') || destination.target.length === 0) {
    return null;
  }

  const display = unescapeMarkdown(label.raw);
  const omitDisplay = display.length === 0 || display === destination.target || display === defaultHeadingDisplay(destination.target);
  let value = embed ? '![[' : '[[';
  value += destination.target;
  if (!omitDisplay) {
    value += '|' + display;
  }
  value += ']]';

  return {
    value,
    end: destination.end + 1,
  };
}

function parseLabel(text: string, openIndex: number): {raw: string, end: number} | null {
  let depth = 1;
  let raw = '';
  for (let i = openIndex + 1; i < text.length; i++) {
    const char = text[i];
    if (char === '\n') {
      return null;
    }
    if (char === '\\') {
      if (i + 1 >= text.length || text[i + 1] === '\n') {
        return null;
      }
      raw += char + text[i + 1];
      i++;
      continue;
    }
    if (char === '[') {
      depth++;
      raw += char;
      continue;
    }
    if (char === ']') {
      depth--;
      if (depth === 0) {
        return {raw, end: i};
      }
      raw += char;
      continue;
    }
    raw += char;
  }

  return null;
}

function parseDestination(text: string, parenIndex: number): {target: string, end: number, hasTitle: boolean} | null {
  let i = parenIndex + 1;
  while (i < text.length && (text[i] === ' ' || text[i] === '\t')) {
    i++;
  }
  if (i >= text.length || text[i] === '\n') {
    return null;
  }

  let raw = '';
  if (text[i] === '<') {
    i++;
    let closed = false;
    for (; i < text.length; i++) {
      const char = text[i];
      if (char === '\n') {
        return null;
      }
      if (char === '\\') {
        if (i + 1 >= text.length || text[i + 1] === '\n') {
          return null;
        }
        raw += char + text[i + 1];
        i++;
        continue;
      }
      if (char === '>') {
        closed = true;
        i++;
        break;
      }
      raw += char;
    }
    if (!closed) {
      return null;
    }
  } else {
    let depth = 0;
    for (; i < text.length; i++) {
      const char = text[i];
      if (char === '\n') {
        return null;
      }
      if (char === '\\') {
        if (i + 1 >= text.length || text[i + 1] === '\n') {
          return null;
        }
        raw += char + text[i + 1];
        i++;
        continue;
      }
      if (char === '(') {
        depth++;
        raw += char;
        continue;
      }
      if (char === ')') {
        if (depth === 0) {
          break;
        }
        depth--;
        raw += char;
        continue;
      }
      if ((char === ' ' || char === '\t') && depth === 0) {
        break;
      }
      raw += char;
    }
  }

  while (i < text.length && (text[i] === ' ' || text[i] === '\t')) {
    i++;
  }
  if (i >= text.length || text[i] === '\n') {
    return null;
  }
  if (text[i] === '"' || text[i] === '\'' || text[i] === '(') {
    return {target: unescapeMarkdown(raw), end: i, hasTitle: true};
  }
  if (text[i] !== ')') {
    return null;
  }

  return {target: unescapeMarkdown(raw), end: i, hasTitle: false};
}

function defaultDisplay(target: string, embed: boolean): string {
  if (embed) {
    return target;
  }

  return defaultHeadingDisplay(target) ?? target;
}

function defaultHeadingDisplay(target: string): string | null {
  const hash = target.indexOf('#');
  if (hash === -1) {
    return null;
  }

  const page = target.slice(0, hash);
  const heading = target.slice(hash + 1);
  if (page.length === 0) {
    return heading;
  }

  return `${page} > ${heading}`;
}

function unescapeMarkdown(value: string): string {
  let result = '';
  for (let i = 0; i < value.length; i++) {
    if (value[i] === '\\' && i + 1 < value.length) {
      result += value[i + 1];
      i++;
      continue;
    }
    result += value[i];
  }

  return result;
}

function isEscaped(text: string, index: number): boolean {
  let slashes = 0;
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) {
    slashes++;
  }

  return slashes % 2 === 1;
}
