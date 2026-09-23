import dedent from 'ts-dedent';
import RemoveMultipleSpaces from '../src/rules/remove-multiple-spaces';
import TrailingSpaces from '../src/rules/trailing-spaces';
import {applyScopedRuleIgnores, getKnownRuleAliases} from '../src/utils/scoped-rule-ignore';

const trailingSpaces = TrailingSpaces.getRule();
const removeMultipleSpaces = RemoveMultipleSpaces.getRule();

function lintBoth(text: string): string {
  return removeMultipleSpaces.apply(trailingSpaces.apply(text));
}

describe('Scoped Rule Ignore Markers', () => {
  it('disables every rule until a bare enable marker', () => {
    const before = dedent`
      trim   
      <!-- linter-disable -->
      keep   
      %% linter-enable %%
      trim again   
    `;
    const after = dedent`
      trim
      <!-- linter-disable -->
      keep   
      %% linter-enable %%
      trim again
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('disables only the listed rules and ignores unknown aliases, case, duplicates, and empty entries', () => {
    const before = dedent`
      <!-- linter-disable Trailing-Spaces, trailing-spaces, not-a-rule, -->
      keep   
      word  word
      <!-- linter-enable -->
      trim   
      word  word
    `;
    const after = dedent`
      <!-- linter-disable Trailing-Spaces, trailing-spaces, not-a-rule, -->
      keep   
      word word
      <!-- linter-enable -->
      trim
      word word
    `;

    expect(lintBoth(before)).toBe(after);
  });

  it('does not treat an empty normalized rule list as disable-all', () => {
    const before = dedent`
      <!-- linter-disable , not-a-rule -->   
      trim   
    `;
    const after = dedent`
      <!-- linter-disable , not-a-rule -->   
      trim
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('preserves marker lines even when that marker does not disable the running rule', () => {
    const before = dedent`
      <!-- linter-disable remove-multiple-spaces -->   
      trim   
    `;
    const after = dedent`
      <!-- linter-disable remove-multiple-spaces -->   
      trim
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('ignores markers that are not on their own line', () => {
    const before = dedent`
      before <!-- linter-disable -->   
      after   
      %% linter-enable %% still text   
    `;
    const after = dedent`
      before <!-- linter-disable -->
      after
      %% linter-enable %% still text
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('ignores markers inside yaml, fenced code, indented code, inline code, and math', () => {
    const before = dedent`
      ---
      title: note
      <!-- linter-disable -->
      ---
      yaml done   

      \`<!-- linter-disable -->\`
      inline done   

      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      fence done   

          <!-- linter-disable -->

      indent done   

      $$
      <!-- linter-disable -->
      $$
      math done   
    `;
    const after = dedent`
      ---
      title: note
      <!-- linter-disable -->
      ---
      yaml done

      \`<!-- linter-disable -->\`
      inline done

      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      fence done

          <!-- linter-disable -->

      indent done

      $$
      <!-- linter-disable -->
      $$
      math done
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('does not close a scope with an enable marker hidden in a code block', () => {
    const before = dedent`
      <!-- linter-disable -->
      kept   
      \`\`\`
      <!-- linter-enable -->
      \`\`\`
      still kept   
      <!-- linter-enable -->
      trimmed   
    `;
    const after = dedent`
      <!-- linter-disable -->
      kept   
      \`\`\`
      <!-- linter-enable -->
      \`\`\`
      still kept   
      <!-- linter-enable -->
      trimmed
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('pops only the nearest scope when enable has no rule list', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      a   
      <!-- linter-disable remove-multiple-spaces -->
      b  b   
      <!-- linter-enable -->
      c   
      <!-- linter-enable -->
      d  d   
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces -->
      a   
      <!-- linter-disable remove-multiple-spaces -->
      b  b   
      <!-- linter-enable -->
      c   
      <!-- linter-enable -->
      d d
    `;

    expect(lintBoth(before)).toBe(after);
  });

  it('removes listed rules from the nearest scope and closes a scope that becomes empty', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      both   
      word  word
      <!-- linter-enable trailing-spaces -->
      trail only   
      word  word
      <!-- linter-enable remove-multiple-spaces -->
      neither   
      word  word
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      both   
      word  word
      <!-- linter-enable trailing-spaces -->
      trail only
      word  word
      <!-- linter-enable remove-multiple-spaces -->
      neither
      word word
    `;

    expect(lintBoth(before)).toBe(after);
  });

  it('re-enables specific rules inside a disable-all scope', () => {
    const before = dedent`
      <!-- linter-disable -->
      both   
      word  word
      <!-- linter-enable trailing-spaces -->
      trail only   
      word  word
      <!-- linter-enable -->
      neither   
      word  word
    `;
    const after = dedent`
      <!-- linter-disable -->
      both   
      word  word
      <!-- linter-enable trailing-spaces -->
      trail only
      word  word
      <!-- linter-enable -->
      neither
      word word
    `;

    expect(lintBoth(before)).toBe(after);
  });

  it('removes a rule only from the nearest scope that disables it', () => {
    const before = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      <!-- linter-enable trailing-spaces -->
      word  word   
      <!-- linter-enable -->
      after   
      <!-- linter-enable -->
      clean  me   
    `;
    const after = dedent`
      <!-- linter-disable trailing-spaces -->
      <!-- linter-disable trailing-spaces, remove-multiple-spaces -->
      <!-- linter-enable trailing-spaces -->
      word  word   
      <!-- linter-enable -->
      after   
      <!-- linter-enable -->
      clean me
    `;

    expect(lintBoth(before)).toBe(after);
  });

  it('applies next-line and next-n disables, including an omitted rule list', () => {
    const before = dedent`
      <!-- linter-disable-next-line trailing-spaces -->
      keep   
      trim   
      %% linter-disable-next-n-lines: 2 %%
      keep all   
      keep all too   
      trim all   
    `;
    const after = dedent`
      <!-- linter-disable-next-line trailing-spaces -->
      keep   
      trim
      %% linter-disable-next-n-lines: 2 %%
      keep all   
      keep all too   
      trim all
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('clamps next-n to EOF and ignores a marker with no following line', () => {
    const before = dedent`
      <!-- linter-disable-next-n-lines: 9 trailing-spaces -->
      only   
    `;
    const after = dedent`
      <!-- linter-disable-next-n-lines: 9 trailing-spaces -->
      only   
    `;
    expect(trailingSpaces.apply(before)).toBe(after);

    const noFollowing = 'trim   \n<!-- linter-disable-next-line trailing-spaces -->   ';
    expect(trailingSpaces.apply(noFollowing)).toBe('trim\n<!-- linter-disable-next-line trailing-spaces -->   ');
  });

  it('ignores next-n markers whose count is not a positive base-10 integer', () => {
    const before = dedent`
      <!-- linter-disable-next-n-lines: 0 trailing-spaces -->
      zero   
      <!-- linter-disable-next-n-lines: -2 trailing-spaces -->
      negative   
      <!-- linter-disable-next-n-lines: 1.5 trailing-spaces -->
      decimal   
      <!-- linter-disable-next-n-lines: 08 trailing-spaces -->
      leading zero   
    `;

    expect(trailingSpaces.apply(before).split('\n')[1]).toBe('zero');
    expect(trailingSpaces.apply(before).split('\n')[3]).toBe('negative');
    expect(trailingSpaces.apply(before).split('\n')[5]).toBe('decimal');
    expect(trailingSpaces.apply(before).split('\n')[7]).toBe('leading zero');
  });

  it('does not let a next-n marker without a colon disable lines', () => {
    const before = dedent`
      <!-- linter-disable-next-n-lines 2 -->   
      trim   
    `;
    const after = dedent`
      <!-- linter-disable-next-n-lines 2 -->
      trim
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('accepts obsidian markers and leading whitespace that is not an indented code block', () => {
    const text = dedent`
      %%linter-disable-next-line%%
      keep   
      trim   
    `;
    expect(trailingSpaces.apply(text)).toBe(dedent`
      %%linter-disable-next-line%%
      keep   
      trim
    `);

    const indentedMarker = '   <!-- linter-disable -->\nkeep   \n<!-- linter-enable -->\ntrim   ';
    expect(trailingSpaces.apply(indentedMarker)).toBe('   <!-- linter-disable -->\nkeep   \n<!-- linter-enable -->\ntrim');
  });

  it('leaves an unclosed disable in effect through end of file', () => {
    const before = dedent`
      trim   
      <!-- linter-disable trailing-spaces -->
      keep   
      still   
    `;
    const after = dedent`
      trim
      <!-- linter-disable trailing-spaces -->
      keep   
      still   
    `;

    expect(trailingSpaces.apply(before)).toBe(after);
  });

  it('can target one rule through the helper without modifying marker lines for another alias', () => {
    const known = getKnownRuleAliases();
    const text = dedent`
      <!-- linter-disable trailing-spaces -->
      aa   
      <!-- linter-enable -->
    `;
    const result = applyScopedRuleIgnores(text, 'remove-multiple-spaces', known, (current) => current.replace(/[ \t]+$/gm, ''));

    expect(result).toBe(dedent`
      <!-- linter-disable trailing-spaces -->
      aa
      <!-- linter-enable -->
    `);
  });
});
