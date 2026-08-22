import {Options, RuleType} from '../rules';
import RuleBuilder, {DropdownOptionBuilder, ExampleBuilder, OptionBuilderBase} from './rule-builder';
import dedent from 'ts-dedent';

type LinkStyleValue = 'no-change' | 'markdown' | 'wiki';

class LinkStyleOptions implements Options {
  linkStyle: LinkStyleValue = 'no-change';
  imageStyle: LinkStyleValue = 'no-change';
}

type ParsedMarkdown = {
  end: number;
  label: string;
  destination: string;
  image: boolean;
};

@RuleBuilder.register
export default class LinkStyle extends RuleBuilder<LinkStyleOptions> {
  constructor() {
    super({
      nameKey: 'rules.link-style.name',
      descriptionKey: 'rules.link-style.description',
      type: RuleType.CONTENT,
    });
  }

  get OptionsClass(): new () => LinkStyleOptions {
    return LinkStyleOptions;
  }

  apply(text: string, options: LinkStyleOptions): string {
    if (options.linkStyle === 'no-change' && options.imageStyle === 'no-change') {
      return text;
    }

    return this.convertProtectedAware(text, options);
  }

  private convertProtectedAware(text: string, options: LinkStyleOptions): string {
    const protectedRanges = this.getProtectedRanges(text);
    let result = '';
    let cursor = 0;
    for (const range of protectedRanges) {
      result += this.convertSegment(text.substring(cursor, range.start), options);
      result += text.substring(range.start, range.end);
      cursor = range.end;
    }
    return result + this.convertSegment(text.substring(cursor), options);
  }

  private convertSegment(text: string, options: LinkStyleOptions): string {
    let result = text;
    if (options.linkStyle === 'markdown' || options.imageStyle === 'markdown') {
      result = result.replace(/!?\[\[([^\]\n]+)\]\]/g, (match, body: string) => {
        const image = match.startsWith('!');
        const style = image ? options.imageStyle : options.linkStyle;
        if (style !== 'markdown') return match;
        const pipe = body.indexOf('|');
        const target = pipe === -1 ? body : body.substring(0, pipe);
        const display = pipe === -1 ? this.defaultDisplay(target) : body.substring(pipe + 1);
        if (image && (display === '300' || display === '300x200')) {
          return `![${this.defaultImageDisplay(target)}](${target})`;
        }
        return `${image ? '!' : ''}[${display}](${target})`;
      });
    }
    if (options.linkStyle === 'wiki' || options.imageStyle === 'wiki') {
      result = this.convertMarkdown(result, options);
    }
    return result;
  }

  private convertMarkdown(text: string, options: LinkStyleOptions): string {
    let result = '';
    let cursor = 0;
    while (cursor < text.length) {
      const parsed = this.parseMarkdown(text, cursor);
      if (!parsed) {
        result += text[cursor++];
        continue;
      }
      const style = parsed.image ? options.imageStyle : options.linkStyle;
      if (style !== 'wiki') {
        result += text.substring(cursor, parsed.end);
      } else {
        const target = parsed.destination;
        const display = parsed.label;
        const headingDisplay = this.defaultDisplay(target);
        const omitted = display === '' || display === target || display === headingDisplay;
        result += `${parsed.image ? '!' : ''}[[${target}${omitted ? '' : `|${display}`}]]`;
      }
      cursor = parsed.end;
    }
    return result;
  }

  private parseMarkdown(text: string, start: number): ParsedMarkdown | null {
    const image = text[start] === '!';
    const labelStart = image ? start + 1 : start;
    if (text[labelStart] !== '[') return null;
    let depth = 0;
    let index = labelStart;
    let labelEnd = -1;
    for (; index < text.length; index++) {
      if (text[index] === '\\') {
        index++;
      } else if (text[index] === '[') {
        depth++;
      } else if (text[index] === ']' && --depth === 0) {
        labelEnd = index;
        break;
      } else if (text[index] === '\n') {
        return null;
      }
    }
    if (labelEnd < 0 || text[labelEnd + 1] !== '(') return null;
    index = labelEnd + 2;
    while (/\s/.test(text[index] ?? '')) index++;
    let destination = '';
    if (text[index] === '<') {
      const close = text.indexOf('>', index + 1);
      if (close < 0 || text.substring(index + 1, close).includes('\n')) return null;
      destination = text.substring(index + 1, close);
      index = close + 1;
    } else {
      const destinationStart = index;
      let parens = 0;
      for (; index < text.length; index++) {
        if (text[index] === '\\') {
          index++;
        } else if (text[index] === '(') {
          parens++;
        } else if (text[index] === ')' && parens > 0) {
          parens--;
        } else if ((text[index] === ' ' || text[index] === '\t' || text[index] === '\n') && parens === 0) {
          break;
        } else if (text[index] === ')' && parens === 0) {
          break;
        }
      }
      destination = text.substring(destinationStart, index);
    }
    while (/\s/.test(text[index] ?? '')) index++;
    if (text[index] !== ')') return null;
    if (text.substring(labelStart, labelEnd + 1).includes('\n') || destination.includes('\n')) return null;
    if (text.substring(labelEnd + 2, index).includes('\n') || destination.includes('://')) return null;
    return {
      end: index + 1,
      label: this.unescapeMarkdown(text.substring(labelStart + 1, labelEnd)),
      destination: this.unescapeMarkdown(destination),
      image,
    };
  }

  private unescapeMarkdown(value: string): string {
    return value.replace(/\\([\\()[\]<> ])/g, '$1');
  }

  private defaultDisplay(target: string): string {
    const hash = target.indexOf('#');
    if (hash < 0) return target;
    return hash === 0 ? target.substring(1) : `${target.substring(0, hash)} > ${target.substring(hash + 1)}`;
  }

  private defaultImageDisplay(target: string): string {
    return target.substring(target.lastIndexOf('/') + 1);
  }

  private getProtectedRanges(text: string): {start: number, end: number}[] {
    const ranges: {start: number, end: number}[] = [];
    const add = (regex: RegExp) => {
      for (const match of text.matchAll(regex)) {
        if (match.index !== undefined) ranges.push({start: match.index, end: match.index + match[0].length});
      }
    };
    add(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/m);
    add(/```[\s\S]*?```/g);
    add(/`[^`\n]*`/g);
    add(/\$\$[\s\S]*?\$\$/g);
    add(/\$[^$\n]+\$/g);
    add(/<%[\s\S]*?%>/g);
    add(/%%[\s\S]*?%%/g);
    add(/<!--[\s\S]*?-->/g);
    add(/^\|.*(?:\r?\n\|.*)*$/gm);
    add(/<!--\s*linter-disable\s*-->[\s\S]*?<!--\s*linter-enable\s*-->/g);
    add(/%%\s*linter-disable\s*%%[\s\S]*?%%\s*linter-enable\s*%%/g);
    ranges.sort((a, b) => a.start - b.start);
    const merged: {start: number, end: number}[] = [];
    for (const range of ranges) {
      const previous = merged[merged.length - 1];
      if (previous && range.start <= previous.end) previous.end = Math.max(previous.end, range.end);
      else merged.push(range);
    }
    return merged;
  }

  get exampleBuilders(): ExampleBuilder<LinkStyleOptions>[] {
    return [
      new ExampleBuilder({
        description: 'Convert wiki links and embeds to markdown',
        before: '[[t]] [[p#h]] ![[f.png|300]]',
        after: '[t](t) [p > h](p#h) ![f.png](f.png)',
        options: {linkStyle: 'markdown', imageStyle: 'markdown'},
      }),
    ];
  }

  get optionBuilders(): OptionBuilderBase<LinkStyleOptions>[] {
    return [
      new DropdownOptionBuilder({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.link-style.name',
        descriptionKey: 'rules.link-style.link-style.description',
        optionsKey: 'linkStyle',
        records: [
          {value: 'no-change', description: ''},
          {value: 'markdown', description: ''},
          {value: 'wiki', description: ''},
        ],
      }),
      new DropdownOptionBuilder({
        OptionsClass: LinkStyleOptions,
        nameKey: 'rules.link-style.image-style.name',
        descriptionKey: 'rules.link-style.image-style.description',
        optionsKey: 'imageStyle',
        records: [
          {value: 'no-change', description: ''},
          {value: 'markdown', description: ''},
          {value: 'wiki', description: ''},
        ],
      }),
    ];
  }
}
