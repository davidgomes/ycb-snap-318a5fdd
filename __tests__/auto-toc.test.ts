import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

const toc = new AutoToc();

function apply(text: string, options?: Parameters<AutoToc['apply']>[1]): string {
  return toc.apply(text, options);
}

describe('Auto TOC exact output', () => {
  it('returns the input unchanged when the start marker is absent', () => {
    const input = '<!-- not a toc -->\n## Hello\n';
    expect(apply(input)).toBe(input);
  });

  it('inserts an end marker and the required blank lines', () => {
    expect(apply('<!-- toc -->\n## Hello\n')).toBe(
        '<!-- toc -->\n\n- [Hello](#hello)\n\n<!-- /toc -->\n\n## Hello\n',
    );
  });

  it('does not add a trailing newline the input did not have', () => {
    expect(apply('<!-- toc -->\n## Hello')).toBe(
        '<!-- toc -->\n\n- [Hello](#hello)\n\n<!-- /toc -->\n\n## Hello',
    );
  });

  it('writes an empty toc with blank lines when no headings qualify', () => {
    expect(apply('<!-- toc -->\n# Title\n')).toBe(
        '<!-- toc -->\n\n<!-- /toc -->\n\n# Title\n',
    );
  });

  it('places an optional title between blank lines', () => {
    expect(apply('<!-- toc -->\n## Hello\n', {title: 'Contents'})).toBe(
        '<!-- toc -->\n\nContents\n\n- [Hello](#hello)\n\n<!-- /toc -->\n\n## Hello\n',
    );
  });

  it('accepts case-insensitive whitespace-tolerant markers and normalizes them', () => {
    expect(apply('<!--   ToC   -->\n<!--   /   TOC   -->\n## Hello\n')).toBe(
        '<!-- toc -->\n\n- [Hello](#hello)\n\n<!-- /toc -->\n\n## Hello\n',
    );
  });

  it('updates the first region and keeps a later end marker', () => {
    expect(apply('<!-- toc -->\nold\n<!-- /toc -->\n<!-- /toc -->\n## Hello\n')).toBe(
        '<!-- toc -->\n\n- [Hello](#hello)\n\n<!-- /toc -->\n\n<!-- /toc -->\n## Hello\n',
    );
  });

  it('is idempotent', () => {
    const once = apply('# Title\n\n<!-- toc -->\n## Section\n### Subsection\n');
    expect(apply(once)).toBe(once);
  });

  it('preserves CRLF line endings', () => {
    expect(apply('<!-- toc -->\r\n## Hello\r\n')).toBe(
        '<!-- toc -->\r\n\r\n- [Hello](#hello)\r\n\r\n<!-- /toc -->\r\n\r\n## Hello\r\n',
    );
  });
});

describe('Auto TOC heading selection', () => {
  it('skips H1 by default, includes deeper ATX headings, and indents from minLevel', () => {
    const input = dedent`
      <!-- toc -->
      # Title
      ## Section
      ### Subsection
      #### Detail
    `;
    expect(apply(input)).toBe(dedent`
      <!-- toc -->

      - [Section](#section)
        - [Subsection](#subsection)
          - [Detail](#detail)

      <!-- /toc -->

      # Title
      ## Section
      ### Subsection
      #### Detail
    `);
  });

  it('honors minLevel, maxLevel, and indentSize', () => {
    const input = '<!-- toc -->\n# A\n## B\n### C\n';
    expect(apply(input, {minLevel: 1, maxLevel: 2, indentSize: 4})).toBe(
        '<!-- toc -->\n\n- [A](#a)\n    - [B](#b)\n\n<!-- /toc -->\n\n# A\n## B\n### C\n',
    );
  });

  it('ignores setext headings', () => {
    const input = '<!-- toc -->\nSetext\n======\n\n## ATX\n';
    expect(apply(input)).toBe(
        '<!-- toc -->\n\n- [ATX](#atx)\n\n<!-- /toc -->\n\nSetext\n======\n\n## ATX\n',
    );
  });

  it('ignores headings inside the toc region', () => {
    const input = '<!-- toc -->\n## Hidden\n<!-- /toc -->\n## Shown\n';
    expect(apply(input)).toBe(
        '<!-- toc -->\n\n- [Shown](#shown)\n\n<!-- /toc -->\n\n## Shown\n',
    );
  });

  it('ignores headings in YAML, fenced code, indented code, and math', () => {
    const input = dedent`
      ---
      # comment
      title: "<!-- toc -->"
      ---

      \`\`\`
      ## In code
      <!-- toc -->
      \`\`\`

          ## Indented code

      $$
      ## In math
      $$

      <!-- toc -->
      ## Shown
    `;
    const output = apply(input);
    expect(output).toContain('- [Shown](#shown)');
    expect(output).not.toContain('[comment]');
    expect(output).not.toContain('[In code]');
    expect(output).not.toContain('[Indented code]');
    expect(output).not.toContain('[In math]');
    expect(output).toContain('# comment');
    expect(output).toContain('## In code');
    expect(output).toContain('## In math');
  });

  it('leaves the note unchanged when the only marker is inside a code block', () => {
    const input = '```\n<!-- toc -->\n```\n## Hello\n';
    expect(apply(input)).toBe(input);
  });

  it('includes a blockquote heading and a three-space indented ATX heading', () => {
    const input = '<!-- toc -->\n> ## Quoted\n   ## Indented\n';
    expect(apply(input)).toBe(
        '<!-- toc -->\n\n- [Quoted](#quoted)\n- [Indented](#indented)\n\n<!-- /toc -->\n\n> ## Quoted\n   ## Indented\n',
    );
  });

  it('excludes literal headings case-insensitively and regex headings', () => {
    const input = '<!-- toc -->\n## Keep\n## Skip Me\n## Skip\n## Appendix One\n';
    expect(apply(input, {excludeHeadings: ['skip me', '/^appendix/']})).toBe(
        '<!-- toc -->\n\n- [Keep](#keep)\n- [Skip](#skip)\n\n<!-- /toc -->\n\n## Keep\n## Skip Me\n## Skip\n## Appendix One\n',
    );
  });
});

describe('Auto TOC anchors and list styles', () => {
  it('slugifies punctuation, underscores, repeated dashes, and trailing hashes', () => {
    const input = [
      '<!-- toc -->',
      '## Hello, World!',
      '## snake_case',
      '## Hello   ---  World',
      '## --Hello--',
      '## Hello ##',
      '## Hello #tag',
      '',
    ].join('\n');
    expect(apply(input)).toBe([
      '<!-- toc -->',
      '',
      '- [Hello, World!](#hello-world)',
      '- [snake_case](#snake_case)',
      '- [Hello   ---  World](#hello-world-1)',
      '- [--Hello--](#hello)',
      '- [Hello](#hello-1)',
      '- [Hello #tag](#hello-tag)',
      '',
      '<!-- /toc -->',
      '',
      '## Hello, World!',
      '## snake_case',
      '## Hello   ---  World',
      '## --Hello--',
      '## Hello ##',
      '## Hello #tag',
      '',
    ].join('\n'));
  });

  it('deduplicates anchors with -1, -2, ...', () => {
    const input = '<!-- toc -->\n## Dup\n## Dup\n## Dup\n';
    expect(apply(input)).toBe(
        '<!-- toc -->\n\n- [Dup](#dup)\n- [Dup](#dup-1)\n- [Dup](#dup-2)\n\n<!-- /toc -->\n\n## Dup\n## Dup\n## Dup\n',
    );
  });

  it('resolves links and removes images and formatting when building anchors', () => {
    const input = [
      '<!-- toc -->',
      '## Read [the docs](https://example.com/a_(b))',
      '## See [ref][id]',
      '## Go to [[My Page|Page]] now',
      '## A ![cat](cat.png) **World**',
      '## B ![[cat.png]] C',
      '## Hello **World**',
      '## Use `code` ==mark== ~~gone~~',
      '## <em>Hi</em>',
      '## \\*literal\\*',
      '',
    ].join('\n');
    const output = apply(input);
    expect(output).toContain('- [Read [the docs](https://example.com/a_(b))](#read-the-docs)');
    expect(output).toContain('- [See [ref][id]](#see-ref)');
    expect(output).toContain('- [Go to [[My Page|Page]] now](#go-to-page-now)');
    expect(output).toContain('- [A ![cat](cat.png) **World**](#a-world)');
    expect(output).toContain('- [B ![[cat.png]] C](#b-c)');
    expect(output).toContain('- [Hello **World**](#hello-world)');
    expect(output).toContain('- [Use `code` ==mark== ~~gone~~](#use-code-mark-gone)');
    expect(output).toContain('- [<em>Hi</em>](#hi)');
    expect(output).toContain('- [\\*literal\\*](#literal)');
  });

  it('strips formatting in the visible toc text when asked', () => {
    const input = '<!-- toc -->\n## Hello **World** ![x](y) [[Note|Alias]]\n';
    expect(apply(input, {stripFormattingInToc: true})).toContain(
        '- [Hello World Alias](#hello-world-alias)',
    );
  });

  it('uses a trailing explicit id as the base anchor and dedupes it', () => {
    const input = '<!-- toc -->\n## Mine {#Custom}\n## Other {#Custom}\n## Plain\n';
    expect(apply(input, {useExplicitIds: true})).toBe(
        '<!-- toc -->\n\n- [Mine](#Custom)\n- [Other](#Custom-1)\n- [Plain](#plain)\n\n<!-- /toc -->\n\n## Mine {#Custom}\n## Other {#Custom}\n## Plain\n',
    );
  });

  it('slugifies a trailing id when explicit ids are off', () => {
    expect(apply('<!-- toc -->\n## Mine {#Custom}\n')).toContain('- [Mine {#Custom}](#mine-custom)');
  });

  it('renders bullet markers and numbered styles', () => {
    const input = '<!-- toc -->\n## A\n### B\n## C\n';
    expect(apply(input, {bulletMarker: '*'})).toContain('* [A](#a)\n  * [B](#b)\n* [C](#c)');
    expect(apply(input, {listStyle: 'number', orderedListStyle: 'always-one'})).toContain(
        '1. [A](#a)\n  1. [B](#b)\n1. [C](#c)',
    );
    expect(apply(input, {listStyle: 'number', orderedListStyle: 'increment'})).toContain(
        '1. [A](#a)\n  2. [B](#b)\n3. [C](#c)',
    );
  });
});

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Rule apply path builds a toc from defaults',
      before: dedent`
        <!-- toc -->
        ## Hello
      `,
      after: dedent`
        <!-- toc -->

        - [Hello](#hello)

        <!-- /toc -->

        ## Hello
      `,
    },
  ],
});
