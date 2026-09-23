import dedent from 'ts-dedent';
import CapitalizeHeadings from '../src/rules/capitalize-headings';
import DefaultLanguageForCodeFences from '../src/rules/default-language-for-code-fences';
import HeadingBlankLines from '../src/rules/heading-blank-lines';
import RemoveMultipleSpaces from '../src/rules/remove-multiple-spaces';
import TrailingSpaces from '../src/rules/trailing-spaces';
import TwoSpacesBetweenLinesWithContent from '../src/rules/two-spaces-between-lines-with-content';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: TrailingSpaces,
  testCases: [
    {
      testName: 'A linter-disable without a rule list disables all rules until the linter-enable',
      before: dedent`
        Line 1   ${''}
        <!-- linter-disable -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
      `,
      after: dedent`
        Line 1
        <!-- linter-disable -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3
      `,
    },
    {
      testName: 'A linter-disable without a rule list disables all rules until the linter-enable when the Obsidian comment format is used',
      before: dedent`
        Line 1   ${''}
        %% linter-disable %%
        Line 2   ${''}
        %%linter-enable%%
        Line 3   ${''}
      `,
      after: dedent`
        Line 1
        %% linter-disable %%
        Line 2   ${''}
        %%linter-enable%%
        Line 3
      `,
    },
    {
      testName: 'A linter-disable without a linter-enable disables the rest of the file',
      before: dedent`
        Line 1   ${''}
        <!-- linter-disable -->
        Line 2   ${''}
        Line 3   ${''}
      `,
      after: dedent`
        Line 1
        <!-- linter-disable -->
        Line 2   ${''}
        Line 3   ${''}
      `,
    },
    {
      testName: 'A rule list is normalized case-insensitively with duplicates and empty entries ignored',
      before: dedent`
        <!-- linter-disable Trailing-Spaces, TRAILING-SPACES,, -->
        Line 1   ${''}
        <!-- linter-enable -->
        Line 2   ${''}
      `,
      after: dedent`
        <!-- linter-disable Trailing-Spaces, TRAILING-SPACES,, -->
        Line 1   ${''}
        <!-- linter-enable -->
        Line 2
      `,
    },
    {
      testName: 'A rule list only disables the rules listed',
      before: dedent`
        <!-- linter-disable capitalize-headings -->
        Line 1   ${''}
        <!-- linter-enable -->
      `,
      after: dedent`
        <!-- linter-disable capitalize-headings -->
        Line 1
        <!-- linter-enable -->
      `,
    },
    {
      testName: 'Unknown rule aliases are ignored while the known rules listed are disabled',
      before: dedent`
        %% linter-disable not-a-rule, trailing-spaces %%
        Line 1   ${''}
      `,
      after: dedent`
        %% linter-disable not-a-rule, trailing-spaces %%
        Line 1   ${''}
      `,
    },
    {
      testName: 'A rule list with only unknown rule aliases has no effect',
      before: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-disable not-a-rule, -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
      `,
      after: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-disable not-a-rule, -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3
      `,
    },
    {
      testName: 'linter-disable-next-line only disables the following line',
      before: dedent`
        <!-- linter-disable-next-line -->
        Line 1   ${''}
        Line 2   ${''}
      `,
      after: dedent`
        <!-- linter-disable-next-line -->
        Line 1   ${''}
        Line 2
      `,
    },
    {
      testName: 'linter-disable-next-n-lines disables the following N lines for the rules listed',
      before: dedent`
        %% linter-disable-next-n-lines: 2 trailing-spaces %%
        Line 1   ${''}
        Line 2   ${''}
        Line 3   ${''}
      `,
      after: dedent`
        %% linter-disable-next-n-lines: 2 trailing-spaces %%
        Line 1   ${''}
        Line 2   ${''}
        Line 3
      `,
    },
    {
      testName: 'linter-disable-next-n-lines is clamped to the end of the file',
      before: dedent`
        Line 1   ${''}
        <!-- linter-disable-next-n-lines: 20 -->
        Line 2   ${''}
        Line 3   ${''}
      `,
      after: dedent`
        Line 1
        <!-- linter-disable-next-n-lines: 20 -->
        Line 2   ${''}
        Line 3   ${''}
      `,
    },
    {
      testName: 'linter-disable-next-n-lines has no effect when N is not a positive base-10 integer',
      before: dedent`
        <!-- linter-disable-next-n-lines: 0 -->
        Line 1   ${''}
        <!-- linter-disable-next-n-lines: -1 -->
        Line 2   ${''}
        <!-- linter-disable-next-n-lines: 1.5 -->
        Line 3   ${''}
        <!-- linter-disable-next-n-lines: two -->
        Line 4   ${''}
        <!-- linter-disable-next-n-lines -->
        Line 5   ${''}
      `,
      after: dedent`
        <!-- linter-disable-next-n-lines: 0 -->
        Line 1
        <!-- linter-disable-next-n-lines: -1 -->
        Line 2
        <!-- linter-disable-next-n-lines: 1.5 -->
        Line 3
        <!-- linter-disable-next-n-lines: two -->
        Line 4
        <!-- linter-disable-next-n-lines -->
        Line 5
      `,
    },
    {
      testName: 'A line-scoped disable on the last line has no effect',
      before: dedent`
        Line 1   ${''}
        <!-- linter-disable-next-line -->
        ${''}
      `,
      after: dedent`
        Line 1
        <!-- linter-disable-next-line -->
        ${''}
      `,
    },
    {
      testName: 'Marker lines are never modified even when they do not disable the rule',
      before: dedent`
        \t<!-- linter-disable capitalize-headings -->   ${''}
        Line 1   ${''}
          %% linter-enable %%\t
        Line 2   ${''}
      `,
      after: dedent`
        \t<!-- linter-disable capitalize-headings -->   ${''}
        Line 1
          %% linter-enable %%\t
        Line 2
      `,
    },
    {
      testName: 'Markers that are not on their own line are not recognized',
      before: dedent`
        Line 1 <!-- linter-disable -->   ${''}
        Line 2   ${''}
        <!-- linter-disable --> Line 3   ${''}
        Line 4   ${''}
      `,
      after: dedent`
        Line 1 <!-- linter-disable -->
        Line 2
        <!-- linter-disable --> Line 3
        Line 4
      `,
    },
    {
      testName: 'Markers in YAML frontmatter, code blocks, inline code, and math blocks are not recognized',
      before: dedent`
        ---
        <!-- linter-disable -->
        ---
        ${''}
        \`\`\`
        <!-- linter-disable -->
        \`\`\`
        ${''}
            %% linter-disable %%
        ${''}
        \`inline code
        %% linter-disable %%
        continues\`
        ${''}
        $$
        %% linter-disable %%
        $$
        Line 1   ${''}
      `,
      after: dedent`
        ---
        <!-- linter-disable -->
        ---
        ${''}
        \`\`\`
        <!-- linter-disable -->
        \`\`\`
        ${''}
            %% linter-disable %%
        ${''}
        \`inline code
        %% linter-disable %%
        continues\`
        ${''}
        $$
        %% linter-disable %%
        $$
        Line 1
      `,
    },
    {
      testName: 'A linter-enable without a rule list closes the most recent open scope',
      before: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-disable trailing-spaces -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
        <!-- linter-enable -->
        Line 4   ${''}
      `,
      after: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-disable trailing-spaces -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
        <!-- linter-enable -->
        Line 4
      `,
    },
    {
      testName: 'A linter-enable with a rule list removes each rule from the nearest open scope that disables it',
      before: dedent`
        <!-- linter-disable trailing-spaces, capitalize-headings -->
        <!-- linter-disable remove-multiple-spaces -->
        Line 1   ${''}
        <!-- linter-enable trailing-spaces -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
      `,
      after: dedent`
        <!-- linter-disable trailing-spaces, capitalize-headings -->
        <!-- linter-disable remove-multiple-spaces -->
        Line 1   ${''}
        <!-- linter-enable trailing-spaces -->
        Line 2
        <!-- linter-enable -->
        Line 3
      `,
    },
    {
      testName: 'A rule-specific scope is closed once all of its rules are enabled',
      before: dedent`
        <!-- linter-disable -->
        <!-- linter-disable trailing-spaces -->
        <!-- linter-enable trailing-spaces -->
        Line 1   ${''}
        <!-- linter-enable -->
        Line 2   ${''}
      `,
      after: dedent`
        <!-- linter-disable -->
        <!-- linter-disable trailing-spaces -->
        <!-- linter-enable trailing-spaces -->
        Line 1   ${''}
        <!-- linter-enable -->
        Line 2
      `,
    },
    {
      testName: 'Specific rules can be enabled in a scope that disables all rules',
      before: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-enable trailing-spaces -->
        Line 2   ${''}
        <!-- linter-enable -->
        Line 3   ${''}
      `,
      after: dedent`
        <!-- linter-disable -->
        Line 1   ${''}
        <!-- linter-enable trailing-spaces -->
        Line 2
        <!-- linter-enable -->
        Line 3
      `,
    },
    {
      testName: 'A linter-enable without an open scope has no effect',
      before: dedent`
        <!-- linter-enable -->   ${''}
        Line 1   ${''}
      `,
      after: dedent`
        <!-- linter-enable -->   ${''}
        Line 1
      `,
    },
  ],
});

ruleTest({
  RuleBuilderClass: CapitalizeHeadings,
  testCases: [
    {
      testName: 'Rules not in a rule list are not disabled',
      before: dedent`
        <!-- linter-disable-next-line trailing-spaces -->
        # first heading
      `,
      after: dedent`
        <!-- linter-disable-next-line trailing-spaces -->
        # First Heading
      `,
    },
    {
      testName: 'A line-scoped disable of the rule only applies to the following line',
      before: dedent`
        %% linter-disable-next-line capitalize-headings %%
        # first heading
        # second heading
      `,
      after: dedent`
        %% linter-disable-next-line capitalize-headings %%
        # first heading
        # Second Heading
      `,
    },
    {
      testName: 'Enabling a different rule in a scope that disables all rules keeps the rule disabled',
      before: dedent`
        <!-- linter-disable -->
        <!-- linter-enable trailing-spaces -->
        # first heading
        <!-- linter-enable -->
        # second heading
      `,
      after: dedent`
        <!-- linter-disable -->
        <!-- linter-enable trailing-spaces -->
        # first heading
        <!-- linter-enable -->
        # Second Heading
      `,
    },
  ],
});

ruleTest({
  RuleBuilderClass: RemoveMultipleSpaces,
  testCases: [
    {
      testName: 'Marker lines are not modified by a rule the marker does not disable',
      before: dedent`
        <!--  linter-disable   trailing-spaces,  capitalize-headings  -->
        Some    text
        %%  linter-enable  %%
      `,
      after: dedent`
        <!--  linter-disable   trailing-spaces,  capitalize-headings  -->
        Some text
        %%  linter-enable  %%
      `,
    },
  ],
});

ruleTest({
  RuleBuilderClass: TwoSpacesBetweenLinesWithContent,
  testCases: [
    {
      testName: 'An Obsidian comment marker between lines of content does not get a line break added to it',
      before: dedent`
        Line 1
        %% linter-disable-next-line trailing-spaces %%
        Line 2
        Line 3
      `,
      after: dedent`
        Line 1
        %% linter-disable-next-line trailing-spaces %%
        Line 2  ${''}
        Line 3
      `,
    },
  ],
});

ruleTest({
  RuleBuilderClass: HeadingBlankLines,
  testCases: [
    {
      testName: 'Blank lines added after a line-scoped disable marker are moved before the marker',
      before: dedent`
        Some text
        <!-- linter-disable-next-line capitalize-headings -->
        # heading
        More text
      `,
      after: dedent`
        Some text
        ${''}
        <!-- linter-disable-next-line capitalize-headings -->
        # heading
        ${''}
        More text
      `,
    },
    {
      testName: 'Blank lines added after a line-scoped disable marker are removed when the marker already follows a blank line',
      before: dedent`
        Some text
        ${''}
        %% linter-disable-next-n-lines: 1 capitalize-headings %%
        # heading
      `,
      after: dedent`
        Some text
        ${''}
        %% linter-disable-next-n-lines: 1 capitalize-headings %%
        # heading
      `,
    },
    {
      testName: 'A line-scoped disable marker that is followed by a blank line keeps it',
      before: dedent`
        Some text
        <!-- linter-disable-next-line capitalize-headings -->
        ${''}
        # heading
      `,
      after: dedent`
        Some text
        <!-- linter-disable-next-line capitalize-headings -->
        ${''}
        # heading
      `,
    },
  ],
});

ruleTest({
  RuleBuilderClass: DefaultLanguageForCodeFences,
  testCases: [
    {
      testName: 'A line-scoped disable that covers part of a code block disables the whole code block',
      before: dedent`
        <!-- linter-disable-next-line default-language-for-code-fences -->
        \`\`\`
        code
        \`\`\`
        ${''}
        \`\`\`
        more code
        \`\`\`
      `,
      after: dedent`
        <!-- linter-disable-next-line default-language-for-code-fences -->
        \`\`\`
        code
        \`\`\`
        ${''}
        \`\`\`js
        more code
        \`\`\`
      `,
      options: {
        defaultLanguage: 'js',
      },
    },
  ],
});
