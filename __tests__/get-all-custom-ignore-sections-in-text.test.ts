import {getAllCustomIgnoreSectionsInText} from '../src/utils/linter-ignore-markers';
import dedent from 'ts-dedent';

type customIgnoresInTextTestCase = {
  name: string,
  text: string,
  expectedCustomIgnoresInText: number,
  expectedPositions: {startIndex:number, endIndex: number}[]
};

const getCustomIgnoreSectionsInTextTestCases: customIgnoresInTextTestCase[] = [
  {
    name: 'when no custom ignore start indicator is present, no positions are returned',
    text: dedent`
      Here is some text
      Here is some more text
    `,
    expectedCustomIgnoresInText: 0,
    expectedPositions: [],
  },
  {
    name: 'when no custom ignore start indicator is present, the enable marker line is still protected',
    text: dedent`
      Here is some text
      <!-- linter-enable -->
      Here is some more text
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 40}],
  },
  {
    name: 'when no custom ignore start indicator is present, the enable marker line is still protected when Obsidian comment format is used',
    text: dedent`
      Here is some text
      %% linter-enable %%
      Here is some more text
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 37}],
  },
  {
    name: 'a simple example of a start and end custom ignore indicator results in the proper start and end positions for the ignore section',
    text: dedent`
      Here is some text
      <!-- linter-disable -->
      This content will be ignored
      So any format put here gets to stay as is
      <!-- linter-enable -->
      More text here...
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 135}],
  },
  {
    name: 'a simple example of a start and end custom ignore indicator results in the proper start and end positions for the ignore section when Obsidian comment format is used',
    text: dedent`
      Here is some text
      %% linter-disable %%
      This content will be ignored
      So any format put here gets to stay as is
      %% linter-enable %%
      More text here...
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 129}],
  },
  {
    name: 'when a custom ignore start indicator is not followed by a custom ignore end indicator in the text, the end is considered to be the end of the text',
    text: dedent`
      Here is some text
      <!-- linter-disable -->
      This content will be ignored
      So any format put here gets to stay as is
      More text here...
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 130}],
  },
  {
    name: 'when a custom ignore start indicator is not followed by a custom ignore end indicator in the text, the end is considered to be the end of the text when Obsidian comment format is used',
    text: dedent`
      Here is some text
      %% linter-disable %%
      This content will be ignored
      So any format put here gets to stay as is
      More text here...
    `,
    expectedCustomIgnoresInText: 1,
    expectedPositions: [{startIndex: 18, endIndex: 127}],
  },
  {
    name: 'when a custom ignore start indicator shows up midline, it is not recognized',
    text: dedent`
      Here is some text<!-- linter-disable -->here is some ignored text<!-- linter-enable -->
      This content will be ignored
      So any format put here gets to stay as is
      More text here...
    `,
    expectedCustomIgnoresInText: 0,
    expectedPositions: [],
  },
  {
    name: 'when a custom ignore start indicator shows up midline, it is not recognized when Obsidian comment format is used',
    text: dedent`
      Here is some text%% linter-disable %%here is some ignored text%% linter-enable %%
      This content will be ignored
      So any format put here gets to stay as is
      More text here...
    `,
    expectedCustomIgnoresInText: 0,
    expectedPositions: [],
  },
  {
    name: 'when a custom ignore start indicator does not follow standalone syntax, it is not recognized',
    text: dedent`
      Here is some text<!-- linter-disable-->here is some ignored text<!-------------         linter-enable ------>
      This content will be ignored
      So any format put here gets to stay as is
      More text here...
    `,
    expectedCustomIgnoresInText: 0,
    expectedPositions: [],
  },
  {
    name: 'multiple matches can be returned',
    text: dedent`
      Here is some text
      ${''}
      <!-- linter-disable -->
      here is some ignored text
      <!-- linter-enable -->
      This content will be formatted
      ${''}
      <!-- linter-disable -->
      We want to ignore the following as we want to preserve its format
        -> level 1
          -> level 1.3
        -> level 2
      Finish
    `,
    expectedCustomIgnoresInText: 2,
    expectedPositions: [{startIndex: 124, endIndex: 263}, {startIndex: 19, endIndex: 91}],
  },
  {
    name: 'multiple matches can be returned when Obsidian comment format is used',
    text: dedent`
      Here is some text
      ${''}
      %% linter-disable %%
      here is some ignored text
      %% linter-enable %%
      This content will be formatted
      ${''}
      %% linter-disable %%
      We want to ignore the following as we want to preserve its format
        -> level 1
          -> level 1.3
        -> level 2
      Finish
    `,
    expectedCustomIgnoresInText: 2,
    expectedPositions: [{startIndex: 118, endIndex: 254}, {startIndex: 19, endIndex: 85}],
  },
  { // relates to https://github.com/platers/obsidian-linter/issues/733
    name: 'multiple matches can be returned with math blocks',
    text: dedent`
      content
      ${''}
      <!-- linter-disable -->
      ${''}
      $$
      abc
      $$
      {#eq:a}
      ${''}
      <!-- linter-enable -->
      ${''}
      content
      ${''}
      <!-- linter-disable -->
      ${''}
      $$
      abc
      $$
      {#eq:b}
      ${''}
      <!-- linter-enable -->
      ${''}
      content
    `,
    expectedCustomIgnoresInText: 2,
    expectedPositions: [{startIndex: 86, endIndex: 152}, {startIndex: 9, endIndex: 75}],
  },
  {
    name: 'multiple matches can be returned with math blocks when Obsidian comment format is used',
    text: dedent`
      content
      ${''}
      %% linter-disable %%
      ${''}
      $$
      abc
      $$
      {#eq:a}
      ${''}
      %% linter-enable %%
      ${''}
      content
      ${''}
      %% linter-disable %%
      ${''}
      $$
      abc
      $$
      {#eq:b}
      ${''}
      %% linter-enable %%
      ${''}
      content
    `,
    expectedCustomIgnoresInText: 2,
    expectedPositions: [{startIndex: 80, endIndex: 140}, {startIndex: 9, endIndex: 69}],
  },
];

describe('Get All Custom Ignore Sections in Text', () => {
  for (const testCase of getCustomIgnoreSectionsInTextTestCases) {
    it(testCase.name, () => {
      const customIgnorePositions = getAllCustomIgnoreSectionsInText(testCase.text);

      expect(customIgnorePositions.length).toEqual(testCase.expectedCustomIgnoresInText);
      expect(customIgnorePositions).toEqual(testCase.expectedPositions);
    });
  }
});
