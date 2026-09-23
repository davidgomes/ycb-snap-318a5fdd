import dedent from 'ts-dedent';
import {RulesRunner} from '../src/rules-runner';
import ConsecutiveBlankLines from '../src/rules/consecutive-blank-lines';
import HeadingBlankLines from '../src/rules/heading-blank-lines';
import TrailingSpaces from '../src/rules/trailing-spaces';
import '../src/rules-registry';
import {Rule} from '../src/rules';

const rulesRunner = new RulesRunner();
const trailingSpaces = TrailingSpaces.getRule();
const headingBlankLines = HeadingBlankLines.getRule();
const consecutiveBlankLines = ConsecutiveBlankLines.getRule();

function apply(rule: Rule, text: string): string {
  return rule.apply(text);
}

describe('Scoped rule ignore markers', () => {
  it('disables every rule until enable when the marker has no rule list', () => {
    const before = dedent`
      trim me   
      <!-- linter-disable -->
      keep me   
      %% linter-enable %%
      trim me too   
    `;
    const after = dedent`
      trim me
      <!-- linter-disable -->
      keep me   
      %% linter-enable %%
      trim me too
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('leaves an unclosed disable in effect through end of file', () => {
    const before = dedent`
      trim me   
      <!-- linter-disable -->
      keep me   
      keep me too   
    `;
    const after = dedent`
      trim me
      <!-- linter-disable -->
      keep me   
      keep me too   
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('disables only the listed rules and ignores unknown aliases, case, and empty entries', () => {
    const before = dedent`
      <!-- linter-disable Trailing-Spaces, not-a-real-rule, trailing-spaces, -->
      keep me   
      <!-- linter-enable -->
      <!-- linter-disable heading-blank-lines -->
      trim me   
      <!-- linter-enable -->
      <!-- linter-disable , , -->
      trim me too   
      <!-- linter-disable not-a-real-rule -->
      trim me three   
      <!-- linter-disable all -->
      trim me four   
    `;
    const after = dedent`
      <!-- linter-disable Trailing-Spaces, not-a-real-rule, trailing-spaces, -->
      keep me   
      <!-- linter-enable -->
      <!-- linter-disable heading-blank-lines -->
      trim me
      <!-- linter-enable -->
      <!-- linter-disable , , -->
      trim me too
      <!-- linter-disable not-a-real-rule -->
      trim me three
      <!-- linter-disable all -->
      trim me four
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('supports nested scopes and re-enabling one rule inside a disable-all scope', () => {
    const before = dedent`
      trim outside   
      <!-- linter-disable -->
      keep all   
      <!-- linter-enable trailing-spaces -->
      trim this rule   
      <!-- linter-disable trailing-spaces -->
      keep again   
      <!-- linter-enable -->
      trim after inner   
      <!-- linter-enable -->
      trim after outer   
    `;
    const after = dedent`
      trim outside
      <!-- linter-disable -->
      keep all   
      <!-- linter-enable trailing-spaces -->
      trim this rule
      <!-- linter-disable trailing-spaces -->
      keep again   
      <!-- linter-enable -->
      trim after inner
      <!-- linter-enable -->
      trim after outer
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('closes only the most recent scope when enable has no rule list', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-disable trailing-spaces -->
      keep inner   
      <!-- linter-enable -->
      keep outer   
      <!-- linter-enable -->
      trim me   
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-disable trailing-spaces -->
      keep inner   
      <!-- linter-enable -->
      keep outer   
      <!-- linter-enable -->
      trim me
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('removes a rule from the nearest scope and closes that scope when it becomes empty', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-disable heading-blank-lines -->
      <!-- linter-enable trailing-spaces -->
      trim me   
      # Still skipped by heading blank lines
      <!-- linter-enable heading-blank-lines -->
      # Formatted heading
    `;

    expect(apply(trailingSpaces, before)).toBe(before.replace('trim me   ', 'trim me'));
    expect(apply(headingBlankLines, before)).toBe(before.replace(
        '<!-- linter-enable heading-blank-lines -->\n# Formatted heading',
        '<!-- linter-enable heading-blank-lines -->\n\n# Formatted heading',
    ));
    expect(apply(headingBlankLines, dedent`
      # Outside
      <!-- linter-disable heading-blank-lines -->
      # Inside
      <!-- linter-enable -->
    `)).toBe(dedent`
      # Outside

      <!-- linter-disable heading-blank-lines -->
      # Inside
      <!-- linter-enable -->
    `);
  });

  it('does not let an unknown enable list close a scope', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-enable not-a-real-rule, -->
      keep me   
      <!-- linter-enable -->
      trim me   
      <!-- linter-enable -->
      trim me too   
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-enable not-a-real-rule, -->
      keep me   
      <!-- linter-enable -->
      trim me
      <!-- linter-enable -->
      trim me too
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('applies line-scoped disables and clamps them to end of file', () => {
    const before = dedent`
      <!-- linter-disable-next-line trailing-spaces -->
      keep one   
      trim one   
      <!-- linter-disable-next-n-lines: 2 trailing-spaces, not-a-real-rule -->
      keep two   
      keep three   
      trim two   
      %% linter-disable-next-n-lines: 9 %%
      keep four   
      <!-- linter-disable-next-line -->
    `;
    const after = dedent`
      <!-- linter-disable-next-line trailing-spaces -->
      keep one   
      trim one
      <!-- linter-disable-next-n-lines: 2 trailing-spaces, not-a-real-rule -->
      keep two   
      keep three   
      trim two
      %% linter-disable-next-n-lines: 9 %%
      keep four   
      <!-- linter-disable-next-line -->
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('ignores line-scoped markers when N is not a positive base-10 integer', () => {
    const before = dedent`
      <!-- linter-disable-next-n-lines: 0 trailing-spaces -->
      trim zero   
      <!-- linter-disable-next-n-lines: -2 trailing-spaces -->
      trim negative   
      <!-- linter-disable-next-n-lines: 1.5 trailing-spaces -->
      trim decimal   
      <!-- linter-disable-next-n-lines: foo -->
      trim word   
      <!-- linter-disable-next-n-lines: 01 trailing-spaces -->
      keep padded   
      trim after padded   
    `;
    const after = dedent`
      <!-- linter-disable-next-n-lines: 0 trailing-spaces -->
      trim zero
      <!-- linter-disable-next-n-lines: -2 trailing-spaces -->
      trim negative
      <!-- linter-disable-next-n-lines: 1.5 trailing-spaces -->
      trim decimal
      <!-- linter-disable-next-n-lines: foo -->
      trim word
      <!-- linter-disable-next-n-lines: 01 trailing-spaces -->
      keep padded   
      trim after padded
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('counts every following physical line, including markers and blank lines', () => {
    const before = dedent`
      <!-- linter-disable-next-n-lines: 1 trailing-spaces -->
      <!-- linter-enable -->
      trim me   
      <!-- linter-disable-next-n-lines: 2 trailing-spaces -->

      keep me   
      trim next   
    `;
    const after = dedent`
      <!-- linter-disable-next-n-lines: 1 trailing-spaces -->
      <!-- linter-enable -->
      trim me
      <!-- linter-disable-next-n-lines: 2 trailing-spaces -->

      keep me   
      trim next
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('never modifies a marker line, even when that marker does not disable the running rule', () => {
    const before = '<!-- linter-disable heading-blank-lines -->   \ntrim me   \n%% linter-enable %%\t';
    const after = '<!-- linter-disable heading-blank-lines -->   \ntrim me\n%% linter-enable %%\t';

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('does not recognize markers that are not alone on the line', () => {
    const before = dedent`
      text <!-- linter-disable trailing-spaces -->
      trim me   
      %% linter-disable %% extra
      trim me too   
    `;
    const after = dedent`
      text <!-- linter-disable trailing-spaces -->
      trim me
      %% linter-disable %% extra
      trim me too
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('does not recognize markers inside YAML, code, inline code, or math', () => {
    const before = dedent`
      ---
      <!-- linter-disable -->
      ---
      trim after yaml   

      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      trim after fence   

      ~~~
      %% linter-disable %%
      ~~~
      trim after tilde   

          <!-- linter-disable -->
      trim after indented code   

         <!-- linter-disable trailing-spaces -->
      keep after three spaces   

      \`
      <!-- linter-disable -->
      \`
      trim after inline code   

      \`<!-- linter-disable -->\`
      trim after one-line inline code   

      $$
      <!-- linter-disable -->
      $$
      trim after math   
    `;
    const after = dedent`
      ---
      <!-- linter-disable -->
      ---
      trim after yaml

      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      trim after fence

      ~~~
      %% linter-disable %%
      ~~~
      trim after tilde

          <!-- linter-disable -->
      trim after indented code

         <!-- linter-disable trailing-spaces -->
      keep after three spaces   

      \`
      <!-- linter-disable -->
      \`
      trim after inline code   

      \`<!-- linter-disable -->\`
      trim after one-line inline code   

      $$
      <!-- linter-disable -->
      $$
      trim after math   
    `;

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('recognizes a tab-indented marker when it is not an indented code block', () => {
    const before = 'paragraph\n\t<!-- linter-disable trailing-spaces -->\nkeep me   \n';
    const after = 'paragraph\n\t<!-- linter-disable trailing-spaces -->\nkeep me   \n';

    expect(apply(trailingSpaces, before)).toBe(after);
  });

  it('preserves disabled blank lines and still collapses blanks outside the scope', () => {
    const before = dedent`
      Some text


      More text
      <!-- linter-disable consecutive-blank-lines -->


      Kept
      <!-- linter-enable -->


      After
    `;
    const after = dedent`
      Some text

      More text
      <!-- linter-disable consecutive-blank-lines -->


      Kept
      <!-- linter-enable -->

      After
    `;

    expect(apply(consecutiveBlankLines, before)).toBe(after);
  });

  it('keeps custom regex out of all-rules ranges and off marker lines', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      keep
      <!-- linter-disable -->
      keep
      <!-- linter-enable -->
      keep
      still here linter-disable
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces -->
      changed
      <!-- linter-disable -->
      keep
      <!-- linter-enable -->
      changed
      still here gone
    `;

    const updated = rulesRunner.runCustomRegexReplacement([
      {label: 'keep', find: 'keep', replace: 'changed', flags: 'g', enabled: true},
      {label: 'marker', find: 'linter-disable', replace: 'gone', flags: 'g', enabled: true},
    ], before);

    expect(updated).toBe(after);
  });

  it('preserves CRLF line endings around a scoped disable', () => {
    const before = 'trim me   \r\n<!-- linter-disable trailing-spaces -->\r\nkeep me   \r\n<!-- linter-enable -->\r\ntrim me too   ';
    const after = 'trim me\r\n<!-- linter-disable trailing-spaces -->\r\nkeep me   \r\n<!-- linter-enable -->\r\ntrim me too';

    expect(apply(trailingSpaces, before)).toBe(after);
  });
});
