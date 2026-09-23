import dedent from 'ts-dedent';
import {getAllCustomIgnoreSectionsInText} from '../src/utils/mdast';
import {RulesRunner} from '../src/rules-runner';
import TrailingSpaces from '../src/rules/trailing-spaces';
import RemoveMultipleSpaces from '../src/rules/remove-multiple-spaces';
import TwoSpacesBetweenLinesWithContent from '../src/rules/two-spaces-between-lines-with-content';

function getIgnoredSections(text: string, ruleAlias?: string): string[] {
  return getAllCustomIgnoreSectionsInText(text, ruleAlias).reverse().map((position) => text.substring(position.startIndex, position.endIndex));
}

type IgnoredSectionsTestCase = {
  name: string,
  text: string,
  ruleAlias?: string,
  expectedIgnoredSections: string[],
};

const ignoredSectionsTestCases: IgnoredSectionsTestCase[] = [
  {
    name: 'a disable marker with a rule list only disables the listed rules',
    text: dedent`
      <!-- linter-disable trailing-spaces, capitalize-headings -->
      disabled
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable trailing-spaces, capitalize-headings -->
      disabled
      <!-- linter-enable -->
    `],
  },
  {
    name: 'a disable marker with a rule list does not disable rules that are not listed, but its marker lines are still ignored',
    text: dedent`
      %% linter-disable trailing-spaces %%
      not disabled
      %% linter-enable %%
      enabled
    `,
    ruleAlias: 'remove-multiple-spaces',
    expectedIgnoredSections: ['%% linter-disable trailing-spaces %%', '%% linter-enable %%'],
  },
  {
    name: 'rule lists are normalized case-insensitively with duplicates, empty entries, and trailing commas ignored',
    text: dedent`
      <!-- linter-disable Trailing-Spaces,, TRAILING-SPACES, -->
      disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable Trailing-Spaces,, TRAILING-SPACES, -->
      disabled
    `],
  },
  {
    name: 'a rule list that only has unknown rules makes the marker have no effect',
    text: dedent`
      <!-- linter-disable not-a-rule -->
      not disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: ['<!-- linter-disable not-a-rule -->'],
  },
  {
    name: 'a rule list that only has empty entries makes the marker have no effect',
    text: dedent`
      <!-- linter-disable , , -->
      not disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: ['<!-- linter-disable , , -->'],
  },
  {
    name: 'unknown rule aliases in a rule list are ignored while the known ones are used',
    text: dedent`
      <!-- linter-disable not-a-rule, trailing-spaces -->
      disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable not-a-rule, trailing-spaces -->
      disabled
    `],
  },
  {
    name: 'a linter-enable with no rule list closes only the most recent scope',
    text: dedent`
      <!-- linter-disable -->
      disabled 1
      <!-- linter-disable header-increment -->
      disabled 2
      <!-- linter-enable -->
      disabled 3
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable -->
      disabled 1
      <!-- linter-disable header-increment -->
      disabled 2
      <!-- linter-enable -->
      disabled 3
      <!-- linter-enable -->
    `],
  },
  {
    name: 'a linter-enable with a rule list re-enables specific rules in a scope that disables all rules',
    text: dedent`
      <!-- linter-disable -->
      disabled
      <!-- linter-enable trailing-spaces -->
      enabled for trailing spaces
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [
      dedent`
        <!-- linter-disable -->
        disabled
        <!-- linter-enable trailing-spaces -->
      `,
      '<!-- linter-enable -->',
    ],
  },
  {
    name: 'rules not re-enabled in a scope that disables all rules stay disabled',
    text: dedent`
      <!-- linter-disable -->
      disabled
      <!-- linter-enable trailing-spaces -->
      still disabled
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'remove-multiple-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable -->
      disabled
      <!-- linter-enable trailing-spaces -->
      still disabled
      <!-- linter-enable -->
    `],
  },
  {
    name: 'a linter-enable with a rule list removes the rule from the nearest scope that disables it and closes that scope when it becomes empty',
    text: dedent`
      <!-- linter-disable -->
      disabled 1
      <!-- linter-disable trailing-spaces -->
      disabled 2
      <!-- linter-enable trailing-spaces -->
      disabled 3
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'remove-multiple-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable -->
      disabled 1
      <!-- linter-disable trailing-spaces -->
      disabled 2
      <!-- linter-enable trailing-spaces -->
      disabled 3
      <!-- linter-enable -->
    `],
  },
  {
    name: 'removing only some rules from a rule-specific scope leaves the scope open for the rest',
    text: dedent`
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      disabled
      <!-- linter-enable trailing-spaces -->
      still disabled for remove multiple spaces
      <!-- linter-enable -->
      enabled
    `,
    ruleAlias: 'remove-multiple-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      disabled
      <!-- linter-enable trailing-spaces -->
      still disabled for remove multiple spaces
      <!-- linter-enable -->
    `],
  },
  {
    name: 'a linter-enable with only unknown rules has no effect',
    text: dedent`
      <!-- linter-disable -->
      disabled
      <!-- linter-enable not-a-rule -->
      still disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable -->
      disabled
      <!-- linter-enable not-a-rule -->
      still disabled
    `],
  },
  {
    name: 'a linter-disable-next-line disables only the next line',
    text: dedent`
      before
      %% linter-disable-next-line %%
      disabled
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      %% linter-disable-next-line %%
      disabled
    `],
  },
  {
    name: 'a linter-disable-next-line with a rule list only disables the listed rules',
    text: dedent`
      <!-- linter-disable-next-line trailing-spaces -->
      not disabled
    `,
    ruleAlias: 'remove-multiple-spaces',
    expectedIgnoredSections: ['<!-- linter-disable-next-line trailing-spaces -->'],
  },
  {
    name: 'a linter-disable-next-n-lines disables the next N lines',
    text: dedent`
      <!-- linter-disable-next-n-lines: 2 trailing-spaces -->
      disabled 1
      disabled 2
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      <!-- linter-disable-next-n-lines: 2 trailing-spaces -->
      disabled 1
      disabled 2
    `],
  },
  {
    name: 'a linter-disable-next-n-lines that goes past the end of the file is clamped to the end of the file',
    text: dedent`
      %% linter-disable-next-n-lines: 20 %%
      disabled 1
      disabled 2
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [dedent`
      %% linter-disable-next-n-lines: 20 %%
      disabled 1
      disabled 2
    `],
  },
  {
    name: 'a line scoped disable on the last line has no effect',
    text: dedent`
      enabled
      <!-- linter-disable-next-line -->
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: ['<!-- linter-disable-next-line -->'],
  },
  ...['0', '-1', '1.5', 'abc', '0x2', ''].map((lineCount): IgnoredSectionsTestCase => ({
    name: `a linter-disable-next-n-lines with an invalid line count of "${lineCount}" has no effect`,
    text: dedent`
      <!-- linter-disable-next-n-lines: ${lineCount} -->
      not disabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [`<!-- linter-disable-next-n-lines: ${lineCount} -->`],
  })),
  {
    name: 'markers are recognized with leading and trailing spaces and tabs',
    text: '   <!-- linter-disable-next-line -->\t \ndisabled\nenabled',
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: ['   <!-- linter-disable-next-line -->\t \ndisabled'],
  },
  {
    name: 'markers that are not on their own line are not recognized',
    text: dedent`
      text <!-- linter-disable -->
      %% linter-disable %% text
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [],
  },
  {
    name: 'markers in YAML, code blocks, inline code, and math blocks are not recognized',
    text: dedent`
      ---
      <!-- linter-disable -->
      ---
      ${''}
      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      ${''}
      ~~~
      %% linter-disable %%
      ~~~
      ${''}
          <!-- linter-disable -->
      ${''}
      \`inline
      %% linter-disable %%
      code\`
      ${''}
      $$
      %% linter-disable %%
      $$
      enabled
    `,
    ruleAlias: 'trailing-spaces',
    expectedIgnoredSections: [],
  },
  {
    name: 'when no rule alias is provided, only markers that disable all rules apply',
    text: dedent`
      <!-- linter-disable trailing-spaces -->
      not disabled
      <!-- linter-enable -->
      <!-- linter-disable -->
      disabled
      <!-- linter-enable trailing-spaces -->
      still disabled
    `,
    expectedIgnoredSections: [
      '<!-- linter-disable trailing-spaces -->',
      dedent`
        <!-- linter-enable -->
        <!-- linter-disable -->
        disabled
        <!-- linter-enable trailing-spaces -->
        still disabled
      `,
    ],
  },
];

describe('Linter ignore markers', () => {
  for (const testCase of ignoredSectionsTestCases) {
    it(testCase.name, () => {
      expect(getIgnoredSections(testCase.text, testCase.ruleAlias)).toEqual(testCase.expectedIgnoredSections);
    });
  }

  it('a rule only skips lines disabled for it', () => {
    const before = dedent`
      trailing  ${''}
      <!-- linter-disable-next-line remove-multiple-spaces -->
      trailing  ${''}
      <!-- linter-disable-next-line trailing-spaces -->
      kept  ${''}
      trailing  ${''}
    `;
    const after = dedent`
      trailing
      <!-- linter-disable-next-line remove-multiple-spaces -->
      trailing
      <!-- linter-disable-next-line trailing-spaces -->
      kept  ${''}
      trailing
    `;

    expect(TrailingSpaces.getRule().apply(before, {})).toBe(after);
  });

  it('marker lines are never modified, even by rules the marker does not disable', () => {
    const before = dedent`
      %%   linter-disable   trailing-spaces   %%  ${''}
      multiple   spaces  ${''}
      %%   linter-enable   %%  ${''}
      multiple   spaces  ${''}
    `;

    expect(TrailingSpaces.getRule().apply(before, {})).toBe(dedent`
      %%   linter-disable   trailing-spaces   %%  ${''}
      multiple   spaces  ${''}
      %%   linter-enable   %%  ${''}
      multiple   spaces
    `);
    expect(RemoveMultipleSpaces.getRule().apply(before, {})).toBe(dedent`
      %%   linter-disable   trailing-spaces   %%  ${''}
      multiple spaces  ${''}
      %%   linter-enable   %%  ${''}
      multiple spaces  ${''}
    `);
  });

  it('marker lines do not get line break indicators added to them', () => {
    const before = dedent`
      %% linter-disable-next-line remove-multiple-spaces %%
      line 1
      line 2
    `;

    expect(TwoSpacesBetweenLinesWithContent.getRule().apply(before, {})).toBe(dedent`
      %% linter-disable-next-line remove-multiple-spaces %%
      line 1  ${''}
      line 2
    `);
  });

  it('custom regex replacements respect only markers that disable all rules', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      replace me
      <!-- linter-enable -->
      <!-- linter-disable-next-line -->
      replace me
      replace me
    `;

    expect(new RulesRunner().runCustomRegexReplacement([{label: '', find: 'replace me', replace: 'replaced', flags: 'g', enabled: true}], before)).toBe(dedent`
      <!-- linter-disable trailing-spaces -->
      replaced
      <!-- linter-enable -->
      <!-- linter-disable-next-line -->
      replace me
      replaced
    `);
  });
});
