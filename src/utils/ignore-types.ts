import {obsidianMultilineCommentRegex, tagWithLeadingWhitespaceRegex, wikiLinkRegex, yamlRegex, escapeDollarSigns, genericLinkRegex, urlRegex, anchorTagRegex, templaterCommandRegex, footnoteDefinitionIndicatorAtStartOfLine} from './regex';
import {getAllCustomIgnoreSectionsInText, getAllTablesInText, getPositions, MDAstTypes} from './mdast';
import type {Position} from 'unist';
import {replaceTextBetweenStartAndEndWithNewValue} from './strings';

export type IgnoreFunction = ((text: string, placeholder: string, ruleAlias?: string) => [string[], string]);
// replacesWholeLines indicates that each replaced value spans entire lines, so anything added to the placeholder's line is discarded when restoring it
export type IgnoreType = {replaceAction: MDAstTypes | RegExp | IgnoreFunction, placeholder: string, replacesWholeLines?: boolean};

export const IgnoreTypes: Record<string, IgnoreType> = {
  // mdast node types
  code: {replaceAction: MDAstTypes.Code, placeholder: '{CODE_BLOCK_PLACEHOLDER}'},
  inlineCode: {replaceAction: MDAstTypes.InlineCode, placeholder: '{INLINE_CODE_BLOCK_PLACEHOLDER}'},
  image: {replaceAction: MDAstTypes.Image, placeholder: '{IMAGE_PLACEHOLDER}'},
  thematicBreak: {replaceAction: MDAstTypes.HorizontalRule, placeholder: '{HORIZONTAL_RULE_PLACEHOLDER}'},
  italics: {replaceAction: MDAstTypes.Italics, placeholder: '{ITALICS_PLACEHOLDER}'},
  bold: {replaceAction: MDAstTypes.Bold, placeholder: '{STRONG_PLACEHOLDER}'},
  list: {replaceAction: MDAstTypes.List, placeholder: '{LIST_PLACEHOLDER}'},
  blockquote: {replaceAction: MDAstTypes.Blockquote, placeholder: '{BLOCKQUOTE_PLACEHOLDER}'},
  math: {replaceAction: MDAstTypes.Math, placeholder: '{MATH_PLACEHOLDER}'},
  inlineMath: {replaceAction: MDAstTypes.InlineMath, placeholder: '{INLINE_MATH_PLACEHOLDER}'},
  html: {replaceAction: MDAstTypes.Html, placeholder: '{HTML_PLACEHOLDER}'},
  heading: {replaceAction: MDAstTypes.Heading, placeholder: '{HEADING_PLACEHOLDER}'},
  // RegExp
  yaml: {replaceAction: yamlRegex, placeholder: escapeDollarSigns('---\n---')},
  wikiLink: {replaceAction: wikiLinkRegex, placeholder: '{WIKI_LINK_PLACEHOLDER}'},
  obsidianMultiLineComments: {replaceAction: obsidianMultilineCommentRegex, placeholder: '{OBSIDIAN_COMMENT_PLACEHOLDER}'},
  footnoteAtStartOfLine: {replaceAction: footnoteDefinitionIndicatorAtStartOfLine, placeholder: '{FOOTNOTE_AT_START_OF_LINE_PLACEHOLDER}'},
  footnoteAfterATask: {replaceAction: /- \[.] (\[\^\w+\]) ?([,.;!:?])/gm, placeholder: '{FOOTNOTE_AFTER_A_TASK_PLACEHOLDER}'},
  url: {replaceAction: urlRegex, placeholder: '{URL_PLACEHOLDER}'},
  anchorTag: {replaceAction: anchorTagRegex, placeholder: '{ANCHOR_PLACEHOLDER}'},
  templaterCommand: {replaceAction: templaterCommandRegex, placeholder: '{TEMPLATER_PLACEHOLDER}'},
  // custom functions
  link: {replaceAction: replaceMarkdownLinks, placeholder: '{REGULAR_LINK_PLACEHOLDER}'},
  tag: {replaceAction: replaceTags, placeholder: '#tag-placeholder'},
  table: {replaceAction: replaceTables, placeholder: '{TABLE_PLACEHOLDER}'},
  customIgnore: {replaceAction: replaceCustomIgnore, placeholder: '{CUSTOM_IGNORE_PLACEHOLDER}', replacesWholeLines: true},
} as const;

/**
 * Replaces the provided ignore types with placeholders, runs the provided function, and then puts the ignored values back.
 * @param {IgnoreType[]} ignoreTypes The types of elements to ignore
 * @param {string} text The text to run the function on
 * @param {function(string): string} func The function to run once the ignore types have been replaced
 * @param {string} [ruleAlias] The alias of the rule being run which is used to determine which linter ignore markers apply
 * @return {string} The text after the function has run with the ignored values put back
 */
export function ignoreListOfTypes(ignoreTypes: IgnoreType[], text: string, func: ((text: string) => string), ruleAlias?: string): string {
  let setOfPlaceholders: {placeholder: string, replacedValues: string[], replacesWholeLines: boolean}[] = [];

  // replace ignore blocks with their placeholders
  let replaceValues: string[] = [];
  for (const ignoreType of ignoreTypes) {
    if (typeof ignoreType.replaceAction === 'string') { // mdast
      [replaceValues, text] = replaceMdastType(text, ignoreType.placeholder, ignoreType.replaceAction);
    } else if (ignoreType.replaceAction instanceof RegExp) {
      [replaceValues, text] = replaceRegex(text, ignoreType.placeholder, ignoreType.replaceAction);
    } else if (typeof ignoreType.replaceAction === 'function') {
      const ignoreFunc: IgnoreFunction = ignoreType.replaceAction;
      [replaceValues, text] = ignoreFunc(text, ignoreType.placeholder, ruleAlias);
    }

    setOfPlaceholders.push({replacedValues: replaceValues, placeholder: ignoreType.placeholder, replacesWholeLines: ignoreType.replacesWholeLines ?? false});
  }

  text = func(text);

  setOfPlaceholders = setOfPlaceholders.reverse();
  // add back values that were replaced with their placeholders
  if (setOfPlaceholders != null && setOfPlaceholders.length > 0) {
    setOfPlaceholders.forEach((replacedInfo: {placeholder: string, replacedValues: string[], replacesWholeLines: boolean}) => {
      replacedInfo.replacedValues.forEach((replacedValue: string) => {
        if (replacedInfo.replacesWholeLines) {
          text = restoreWholeLinePlaceholder(text, replacedInfo.placeholder, replacedValue);
          return;
        }

        // Regex was added to fix capitalization issue  where another rule made the text not match the original place holder's case
        // see https://github.com/platers/obsidian-linter/issues/201
        text = text.replace(new RegExp(replacedInfo.placeholder, 'i'), escapeDollarSigns(replacedValue));
      });
    });
  }

  return text;
}

/**
 * Puts back the first instance of a placeholder that originally took up entire lines.
 * Whitespace or line break indicators that a rule added to the placeholder's line are removed
 * so that the original lines are restored exactly as they were.
 * @param {string} text The text to restore the placeholder in
 * @param {string} placeholder The placeholder to restore
 * @param {string} replacedValue The original value of the placeholder
 * @return {string} The text with the first instance of the placeholder restored
 */
function restoreWholeLinePlaceholder(text: string, placeholder: string, replacedValue: string): string {
  const placeholderMatch = new RegExp(placeholder, 'i').exec(text);
  if (!placeholderMatch) {
    return text;
  }

  let startIndex = placeholderMatch.index;
  let endIndex = startIndex + placeholderMatch[0].length;
  const lineStart = text.lastIndexOf('\n', startIndex - 1) + 1;
  let lineEnd = text.indexOf('\n', endIndex);
  if (lineEnd === -1) {
    lineEnd = text.length;
  }

  if (/^[ \t]*$/.test(text.substring(lineStart, startIndex))) {
    startIndex = lineStart;
  }

  if (/^(?:[ \t]|\\|<br\/?>)*$/.test(text.substring(endIndex, lineEnd))) {
    endIndex = lineEnd;
  }

  return replaceTextBetweenStartAndEndWithNewValue(text, startIndex, endIndex, replacedValue);
}

/**
 * Replaces all mdast type instances in the given text with a placeholder.
 * @param {string} text The text to replace the given mdast node type in
 * @param {string} placeholder The placeholder to use
 * @param {MDAstTypes} type The type of node to ignore by replacing with the specified placeholder
 * @return {string} The text with mdast nodes types specified replaced
 * @return {string[]} The mdast nodes values replaced
 */
function replaceMdastType(text: string, placeholder: string, type: MDAstTypes): [string[], string] {
  let positions: Position[] = getPositions(type, text);
  const replacedValues: string[] = [];

  if (type === MDAstTypes.List) {
    positions = removeOverlappingPositions(positions);
  }

  for (const position of positions) {
    const valueToReplace = text.substring(position.start.offset, position.end.offset);
    replacedValues.push(valueToReplace);
  }

  for (const position of positions) {
    text = replaceTextBetweenStartAndEndWithNewValue(text, position.start.offset, position.end.offset, placeholder);
  }

  // Reverse the replaced values so that they are in the same order as the original text
  replacedValues.reverse();

  return [replacedValues, text];
}

/**
 * Replaces all regex matches in the given text with a placeholder.
 * @param {string} text The text to replace the regex matches in
 * @param {string} placeholder The placeholder to use
 * @param {RegExp} regex The regex to use to find what to replace with the placeholder
 * @return {string} The text with regex matches replaced
 * @return {string[]} The regex matches replaced
 */
function replaceRegex(text: string, placeholder: string, regex: RegExp): [string[], string] {
  const regexMatches = text.match(regex);
  const textMatches: string[] = [];
  if (regex.flags.includes('g')) {
    text = text.replaceAll(regex, placeholder);

    if (regexMatches) {
      for (const matchText of regexMatches) {
        textMatches.push(matchText);
      }
    }
  } else {
    text = text.replace(regex, placeholder);

    if (regexMatches) {
      textMatches.push(regexMatches[0]);
    }
  }

  return [textMatches, text];
}

/**
 * Replaces all markdown links in the given text with a placeholder.
 * @param {string} text The text to replace links in
 * @param {string} regularLinkPlaceholder The placeholder to use for regular markdown links
 * @return {string} The text with links replaced
 * @return {string[]} The regular markdown links replaced
 */
function replaceMarkdownLinks(text: string, regularLinkPlaceholder: string): [string[], string] {
  const positions: Position[] = getPositions(MDAstTypes.Link, text);
  const replacedRegularLinks: string[] = [];


  const positionsToReplace: Position [] = [];
  for (const position of positions) {
    if (position == undefined) {
      continue;
    }

    const regularLink = text.substring(position.start.offset, position.end.offset);
    // skip links that are not in markdown format
    if (!regularLink.match(genericLinkRegex)) {
      continue;
    }

    positionsToReplace.push(position);
    replacedRegularLinks.push(regularLink);
  }

  for (const position of positionsToReplace) {
    text = replaceTextBetweenStartAndEndWithNewValue(text, position.start.offset, position.end.offset, regularLinkPlaceholder);
  }

  // Reverse the regular links so that they are in the same order as the original text
  replacedRegularLinks.reverse();

  return [replacedRegularLinks, text];
}

function replaceTags(text: string, placeholder: string): [string[], string] {
  const replacedValues: string[] = [];

  text = text.replace(tagWithLeadingWhitespaceRegex, (_, whitespace, tag) => {
    replacedValues.push(tag);
    return whitespace + placeholder;
  });

  return [replacedValues, text];
}

function replaceTables(text: string, tablePlaceholder: string): [string[], string] {
  const tablePositions = getAllTablesInText(text);

  const replacedTables: string[] = new Array(tablePositions.length);
  let index = 0;
  const length = replacedTables.length;
  for (const tablePosition of tablePositions) {
    replacedTables[length - 1 - index++] = text.substring(tablePosition.startIndex, tablePosition.endIndex);
  }

  for (const tablePosition of tablePositions) {
    text = replaceTextBetweenStartAndEndWithNewValue(text, tablePosition.startIndex, tablePosition.endIndex, tablePlaceholder);
  }

  return [replacedTables, text];
}


function replaceCustomIgnore(text: string, customIgnorePlaceholder: string, ruleAlias?: string): [string[], string] {
  const customIgnorePositions = getAllCustomIgnoreSectionsInText(text, ruleAlias);

  const replacedSections: string[] = new Array(customIgnorePositions.length);
  let index = 0;
  const length = replacedSections.length;
  for (const customIgnorePosition of customIgnorePositions) {
    replacedSections[length - 1 - index++] = text.substring(customIgnorePosition.startIndex, customIgnorePosition.endIndex);
  }

  for (const customIgnorePosition of customIgnorePositions) {
    text = replaceTextBetweenStartAndEndWithNewValue(text, customIgnorePosition.startIndex, customIgnorePosition.endIndex, customIgnorePlaceholder);
  }

  return [replacedSections, text];
}

function removeOverlappingPositions(positions: Position[]): Position[] {
  if (positions.length < 2) {
    return positions;
  }

  let lastPosition: Position = positions.pop();
  let currentPosition: Position = null;
  const result: Position[] = [lastPosition];
  while (positions.length > 0) {
    currentPosition = positions.pop();
    if (lastPosition.start.offset >= currentPosition.end.offset || currentPosition.start.offset >= lastPosition.end.offset) {
      result.unshift(currentPosition);
      lastPosition = currentPosition;
    }
  }

  return result;
}
