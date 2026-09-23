import dedent from 'ts-dedent';
import RemoveMultipleSpaces from '../src/rules/remove-multiple-spaces';
import {ruleTest} from './common';
import {getCustomIgnoreTypeForRule, IgnoreTypes, ignoreListOfTypes} from '../src/utils/ignore-types';

ruleTest({
  RuleBuilderClass: RemoveMultipleSpaces,
  testCases: [
    {
      testName: 'A rule specific disable only disables the listed rules',
      before: dedent`
        a  b
        <!-- linter-disable remove-multiple-spaces -->
        c  d
        <!-- linter-enable -->
        e  f
        %% linter-disable capitalize-headings %%
        g  h
        %% linter-enable %%
      `,
      after: dedent`
        a b
        <!-- linter-disable remove-multiple-spaces -->
        c  d
        <!-- linter-enable -->
        e f
        %% linter-disable capitalize-headings %%
        g h
        %% linter-enable %%
      `,
    },
    {
      testName: 'Rule lists are case-insensitive, deduplicated, and ignore empty entries and unknown aliases',
      before: dedent`
        <!-- linter-disable Remove-Multiple-Spaces, remove-multiple-spaces,, unknown-rule, -->
        a  b
      `,
      after: dedent`
        <!-- linter-disable Remove-Multiple-Spaces, remove-multiple-spaces,, unknown-rule, -->
        a  b
      `,
    },
    {
      testName: 'A disable with only unknown rules has no effect so a following enable closes the previous scope',
      before: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-disable unknown-rule -->
        c  d
        <!-- linter-enable -->
        e  f
      `,
      after: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-disable unknown-rule -->
        c  d
        <!-- linter-enable -->
        e f
      `,
    },
    {
      testName: 'Nested scopes use stack semantics for enable without a rule list',
      before: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-disable capitalize-headings -->
        c  d
        <!-- linter-enable -->
        e  f
        <!-- linter-enable -->
        g  h
      `,
      after: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-disable capitalize-headings -->
        c  d
        <!-- linter-enable -->
        e  f
        <!-- linter-enable -->
        g h
      `,
    },
    {
      testName: 'Disabling all rules and re-enabling a specific rule within the scope is supported',
      before: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-enable remove-multiple-spaces -->
        c  d
        <!-- linter-disable remove-multiple-spaces -->
        e  f
        <!-- linter-enable -->
        g  h
        <!-- linter-enable -->
        i  j
      `,
      after: dedent`
        <!-- linter-disable -->
        a  b
        <!-- linter-enable remove-multiple-spaces -->
        c d
        <!-- linter-disable remove-multiple-spaces -->
        e  f
        <!-- linter-enable -->
        g h
        <!-- linter-enable -->
        i j
      `,
    },
    {
      testName: 'An enable with a rule list removes the rule from the nearest scope that disables it and closes emptied rule specific scopes',
      before: dedent`
        <!-- linter-disable remove-multiple-spaces -->
        <!-- linter-disable capitalize-headings -->
        a  b
        <!-- linter-enable remove-multiple-spaces -->
        c  d
        <!-- linter-enable -->
        e  f
      `,
      after: dedent`
        <!-- linter-disable remove-multiple-spaces -->
        <!-- linter-disable capitalize-headings -->
        a  b
        <!-- linter-enable remove-multiple-spaces -->
        c d
        <!-- linter-enable -->
        e f
      `,
    },
    {
      testName: 'Disable next line and disable next n lines only affect the requested lines',
      before: dedent`
        <!-- linter-disable-next-line remove-multiple-spaces -->
        a  b
        c  d
        %% linter-disable-next-n-lines: 2 %%
        e  f
        g  h
        i  j
      `,
      after: dedent`
        <!-- linter-disable-next-line remove-multiple-spaces -->
        a  b
        c d
        %% linter-disable-next-n-lines: 2 %%
        e  f
        g  h
        i j
      `,
    },
    {
      testName: 'Disable next n lines with an invalid count has no effect and ranges past the end of the file are clamped',
      before: dedent`
        <!-- linter-disable-next-n-lines: 0 -->
        a  b
        <!-- linter-disable-next-n-lines: -1 -->
        c  d
        <!-- linter-disable-next-n-lines: abc -->
        e  f
        <!-- linter-disable-next-n-lines: 50 -->
        g  h
      `,
      after: dedent`
        <!-- linter-disable-next-n-lines: 0 -->
        a b
        <!-- linter-disable-next-n-lines: -1 -->
        c d
        <!-- linter-disable-next-n-lines: abc -->
        e f
        <!-- linter-disable-next-n-lines: 50 -->
        g  h
      `,
    },
    {
      testName: 'Markers are only recognized on standalone lines',
      before: dedent`
        text <!-- linter-disable -->
        a  b
        \t <!-- linter-disable -->  ${''}
        c  d
      `,
      after: dedent`
        text <!-- linter-disable -->
        a b
        \t <!-- linter-disable -->  ${''}
        c  d
      `,
    },
    {
      testName: 'Markers in YAML frontmatter, code blocks, and math blocks are ignored',
      before: dedent`
        ---
        key: value
        <!-- linter-disable -->
        ---
        a  b
        \`\`\`
        <!-- linter-disable -->
        \`\`\`
        c  d
        ${''}
            %% linter-disable %%
        ${''}
        $$
        <!-- linter-disable -->
        $$
        e  f
      `,
      after: dedent`
        ---
        key: value
        <!-- linter-disable -->
        ---
        a b
        \`\`\`
        <!-- linter-disable -->
        \`\`\`
        c d
        ${''}
            %% linter-disable %%
        ${''}
        $$
        <!-- linter-disable -->
        $$
        e f
      `,
    },
    {
      testName: 'Marker lines are never modified even when they do not disable the rule',
      before: dedent`
        <!--   linter-disable   capitalize-headings   -->
        a  b
        %%   linter-enable   %%
      `,
      after: dedent`
        <!--   linter-disable   capitalize-headings   -->
        a b
        %%   linter-enable   %%
      `,
    },
  ],
});

describe('Linter marker ignore ranges', () => {
  it('replaces marker lines and disabled lines with a single placeholder per contiguous block', () => {
    const text = dedent`
      a
      <!-- linter-disable-next-line trailing-spaces -->
      b
      c
      %% linter-disable-next-line %%
    `;

    ignoreListOfTypes([getCustomIgnoreTypeForRule('trailing-spaces')], text, (textAfterIgnore: string) => {
      expect(textAfterIgnore).toEqual(dedent`
        a
        {CUSTOM_IGNORE_PLACEHOLDER}
        c
        {CUSTOM_IGNORE_PLACEHOLDER}
      `);

      return textAfterIgnore;
    });
  });

  it('only applies markers that disable all rules when no specific rule is being run', () => {
    const text = dedent`
      <!-- linter-disable trailing-spaces -->
      a
      <!-- linter-enable -->
      <!-- linter-disable -->
      b
      <!-- linter-enable trailing-spaces -->
      c
    `;

    ignoreListOfTypes([IgnoreTypes.customIgnore], text, (textAfterIgnore: string) => {
      expect(textAfterIgnore).toEqual(dedent`
        {CUSTOM_IGNORE_PLACEHOLDER}
        a
        {CUSTOM_IGNORE_PLACEHOLDER}
      `);

      return textAfterIgnore;
    });
  });

  it('does not include the trailing newline at the end of the file in an unclosed scope', () => {
    const text = 'a\n<!-- linter-disable -->\nb\n';

    const result = ignoreListOfTypes([IgnoreTypes.customIgnore], text, (textAfterIgnore: string) => {
      expect(textAfterIgnore).toEqual('a\n{CUSTOM_IGNORE_PLACEHOLDER}\n');

      return textAfterIgnore;
    });

    expect(result).toEqual(text);
  });
});
