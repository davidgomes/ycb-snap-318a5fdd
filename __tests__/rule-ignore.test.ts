import '../src/rules-registry';
import {getIgnoreSpansForRule, parseStandaloneMarker} from '../src/utils/rule-ignore';
import {rulesDict} from '../src/rules';
import {ignoreListOfTypes, IgnoreTypes} from '../src/utils/ignore-types';
import dedent from 'ts-dedent';

describe('parseStandaloneMarker', () => {
  it('rejects markers that share a line with other text', () => {
    expect(parseStandaloneMarker('text <!-- linter-disable -->')).toBeNull();
    expect(parseStandaloneMarker('<!-- linter-disable --> text')).toBeNull();
    expect(parseStandaloneMarker('\t%% linter-enable %% trailing')).toBeNull();
  });

  it('accepts html and obsidian markers with surrounding whitespace', () => {
    expect(parseStandaloneMarker('  <!-- linter-disable -->')).toMatchObject({kind: 'disable', rules: null});
    expect(parseStandaloneMarker('\t%% linter-enable %%')).toMatchObject({kind: 'enable', rules: null});
  });

  it('normalizes rule lists and drops unknown or empty entries', () => {
    const alias = rulesDict['trailing-spaces'].alias;
    const parsed = parseStandaloneMarker(`<!-- linter-disable ${alias.toUpperCase()}, ${alias}, , not-a-rule, -->`);
    expect(parsed).toMatchObject({kind: 'disable', rules: [alias]});
    expect(parseStandaloneMarker('<!-- linter-disable not-a-rule, -->')).toMatchObject({kind: 'disable', rules: []});
    expect(parseStandaloneMarker('<!-- linter-disable -->')).toMatchObject({kind: 'disable', rules: null});
  });

  it('requires a positive integer for next-n-lines', () => {
    expect(parseStandaloneMarker('<!-- linter-disable-next-n-lines: 2 -->')).toMatchObject({kind: 'disable-next-n-lines', lineCount: 2, rules: null});
    expect(parseStandaloneMarker('%% linter-disable-next-n-lines: 0 %%')).toMatchObject({kind: 'disable-next-n-lines', lineCount: null});
    expect(parseStandaloneMarker('<!-- linter-disable-next-n-lines: 01 -->')).toMatchObject({lineCount: 1});
    expect(parseStandaloneMarker('<!-- linter-disable-next-n-lines: -3 -->')).toMatchObject({lineCount: null});
    expect(parseStandaloneMarker('<!-- linter-disable-next-n-lines: 1e2 -->')).toMatchObject({lineCount: null});
    expect(parseStandaloneMarker('<!-- linter-disable-next-n-lines -->')).toBeNull();
  });
});

describe('scoped rule ignore', () => {
  const trailing = 'trailing-spaces';
  const heading = 'header-increment';

  function protectedText(text: string, ruleAlias: string | null): string {
    const spans = getIgnoreSpansForRule(text, ruleAlias);
    let result = text;
    for (const span of spans) {
      result = result.slice(0, span.startIndex) + '#'.repeat(span.endIndex - span.startIndex) + result.slice(span.endIndex);
    }
    return result;
  }

  it('disables every rule until the matching enable, and leaves marker lines in the ignored span', () => {
    const text = dedent`
      keep  ${''}
      <!-- linter-disable -->
      stay  ${''}
      %% linter-enable %%
      keep  ${''}
    `;
    const masked = protectedText(text, trailing);
    expect(masked.startsWith('keep  ')).toBe(true);
    expect(masked).not.toContain('stay');
    expect(masked).not.toContain('linter-disable');
    expect(masked.endsWith('keep  ')).toBe(true);
  });

  it('disables only the listed rules', () => {
    const text = dedent`
      <!-- linter-disable ${trailing} -->
      edited  ${''}
      <!-- linter-enable -->
    `;
    expect(protectedText(text, trailing)).not.toContain('edited');
    expect(protectedText(text, heading)).toContain('edited');
    expect(protectedText(text, heading)).not.toContain('linter-disable');
  });

  it('ignores a marker whose rule list normalizes to empty', () => {
    const text = dedent`
      <!-- linter-disable not-a-real-rule, -->
      edited  ${''}
    `;
    expect(protectedText(text, trailing)).toContain('edited');
    expect(protectedText(text, trailing)).not.toContain('linter-disable');
  });

  it('supports nested scopes and re-enabling one rule inside a blanket disable', () => {
    const text = dedent`
      <!-- linter-disable -->
      both  ${''}
      <!-- linter-disable ${trailing} -->
      inner  ${''}
      <!-- linter-enable ${trailing} -->
      after-inner  ${''}
      <!-- linter-enable ${trailing} -->
      reenabled  ${''}
      <!-- linter-enable -->
      done  ${''}
    `;
    expect(protectedText(text, trailing)).not.toContain('both');
    expect(protectedText(text, trailing)).not.toContain('inner');
    expect(protectedText(text, trailing)).not.toContain('after-inner');
    expect(protectedText(text, trailing)).toContain('reenabled');
    expect(protectedText(text, heading)).not.toContain('reenabled');
    expect(protectedText(text, heading)).not.toContain('both');
    expect(protectedText(text, trailing)).toContain('done');
    expect(protectedText(text, heading)).toContain('done');
  });

  it('pops only the innermost scope when enable has no rule list', () => {
    const text = dedent`
      <!-- linter-disable ${trailing} -->
      outer  ${''}
      <!-- linter-disable ${heading} -->
      inner  ${''}
      <!-- linter-enable -->
      still  ${''}
      <!-- linter-enable -->
      free  ${''}
    `;
    expect(protectedText(text, trailing)).not.toContain('still');
    expect(protectedText(text, heading)).toContain('still');
    expect(protectedText(text, trailing)).toContain('free');
  });

  it('closes a rule-specific scope once its rules are all re-enabled', () => {
    const text = dedent`
      <!-- linter-disable ${trailing}, ${heading} -->
      both  ${''}
      <!-- linter-enable ${trailing}, ${heading} -->
      free  ${''}
    `;
    expect(protectedText(text, trailing)).not.toContain('both');
    expect(protectedText(text, heading)).not.toContain('both');
    expect(protectedText(text, trailing)).toContain('free');
    expect(protectedText(text, heading)).toContain('free');
  });

  it('applies next-line and next-n-lines disables and clamps at eof', () => {
    const text = dedent`
      <!-- linter-disable-next-line ${trailing} -->
      one  ${''}
      two  ${''}
      %% linter-disable-next-n-lines: 2 %%
      three  ${''}
      four  ${''}
      five  ${''}
      <!-- linter-disable-next-n-lines: 5 -->
      last  ${''}
    `;
    const masked = protectedText(text, null);
    expect(masked).toContain('one');
    expect(masked).toContain('two');
    expect(masked).not.toContain('three');
    expect(masked).not.toContain('four');
    expect(masked).toContain('five');
    expect(masked).not.toContain('last');

    const trailingMasked = protectedText(text, trailing);
    expect(trailingMasked).not.toContain('one');
    expect(trailingMasked).toContain('two');
  });

  it('does not apply a line-scoped disable when there is no following line or the count is invalid', () => {
    expect(protectedText('<!-- linter-disable-next-line -->', trailing)).not.toContain('linter-disable');
    const invalid = dedent`
      <!-- linter-disable-next-n-lines: 0 -->
      keep  ${''}
    `;
    expect(protectedText(invalid, trailing)).toContain('keep');
  });

  it('ignores markers inside frontmatter, code, inline code, and math blocks', () => {
    const text = dedent`
      ---
      <!-- linter-disable -->
      ---
      keep  ${''}

      \`\`\`
      <!-- linter-disable -->
      \`\`\`
      keep  ${''}

          <!-- linter-disable -->
      keep  ${''}

      \`<!-- linter-disable -->\`
      keep  ${''}

      $$
      <!-- linter-disable -->
      $$
      keep  ${''}
    `;
    const masked = protectedText(text, trailing);
    expect(masked).toContain('<!-- linter-disable -->');
    expect(masked.match(/keep {2}/g)).toHaveLength(5);
  });

  it('keeps marker lines unchanged when a rule edits the surrounding text', () => {
    const text = dedent`
      hello   ${''}
      <!-- linter-disable-next-line -->
      stay   
      <!-- linter-enable not-a-rule -->
      trim   
    `;
    const result = ignoreListOfTypes([IgnoreTypes.customIgnore], text, (current) => current.replace(/[ \t]+$/gm, ''), trailing);
    expect(result).toContain('<!-- linter-disable-next-line -->');
    expect(result).toContain('<!-- linter-enable not-a-rule -->');
    expect(result).toContain('stay   ');
    expect(result).toContain('trim');
    expect(result).not.toContain('trim ');
    expect(result.startsWith('hello\n') || result.startsWith('hello')).toBe(true);
    expect(result.split('\n')[0]).toBe('hello');
  });
});
