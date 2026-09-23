import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Leaves the file unchanged when the toc marker is absent',
      before: dedent`
        # Title
        ${''}
        ## Section
      `,
      after: dedent`
        # Title
        ${''}
        ## Section
      `,
    },
    {
      testName: 'Accepts case-insensitive and whitespace-tolerant markers and inserts a missing end marker',
      before: dedent`
        <!--TOC-->
        ${''}
        ## Alpha
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Alpha](#alpha)
        ${''}
        <!-- /toc -->
        ${''}
        ## Alpha
      `,
    },
    {
      testName: 'Uses the first start marker and the first end marker after it',
      before: dedent`
        <!-- /toc -->
        <!--  toc  -->
        old
        <!--   /   toc   -->
        ${''}
        ## Outside
        ${''}
        <!-- toc -->
        <!-- /toc -->
      `,
      after: dedent`
        <!-- /toc -->
        <!-- toc -->
        ${''}
        - [Outside](#outside)
        ${''}
        <!-- /toc -->
        ${''}
        ## Outside
        ${''}
        <!-- toc -->
        <!-- /toc -->
      `,
    },
    {
      testName: 'Does not include setext headings, heading level 1, or headings outside the level range',
      before: dedent`
        <!-- toc -->
        ${''}
        # H1
        ${''}
        Setext
        =====
        ${''}
        ## H2
        #### Too deep
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [H2](#h2)
        ${''}
        <!-- /toc -->
        ${''}
        # H1
        ${''}
        Setext
        =====
        ${''}
        ## H2
        #### Too deep
      `,
      options: {
        maxLevel: 3,
      },
    },
    {
      testName: 'Builds anchors from link display text, drops images and formatting, and dedupes',
      before: dedent`
        <!-- toc -->
        ${''}
        ## See [the docs](https://example.com)
        ## Picture ![[cat.png]] of cats
        ## ![logo](logo.png) Logo
        ## __Bold__ _italic_ \`code\`
        ## See [the docs](https://example.com)
        ## Hello   World!
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [See the docs](#see-the-docs)
        - [Picture  of cats](#picture-of-cats)
        - [Logo](#logo)
        - [__Bold__ _italic_ \`code\`](#bold-italic-code)
        - [See the docs](#see-the-docs-1)
        - [Hello   World!](#hello-world)
        ${''}
        <!-- /toc -->
        ${''}
        ## See [the docs](https://example.com)
        ## Picture ![[cat.png]] of cats
        ## ![logo](logo.png) Logo
        ## __Bold__ _italic_ \`code\`
        ## See [the docs](https://example.com)
        ## Hello   World!
      `,
    },
    {
      testName: 'Strips formatting in the table of contents when enabled and keeps it otherwise by default above',
      before: dedent`
        <!-- toc -->
        ${''}
        ## **Bold** and ==hi==
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Bold and hi](#bold-and-hi)
        ${''}
        <!-- /toc -->
        ${''}
        ## **Bold** and ==hi==
      `,
      options: {
        stripFormattingInToc: true,
      },
    },
    {
      testName: 'Numbers every item as 1 or increments across all items',
      before: dedent`
        <!-- toc -->
        ${''}
        ## One
        ### Nested
        ## Two
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        1. [One](#one)
          1. [Nested](#nested)
        1. [Two](#two)
        ${''}
        <!-- /toc -->
        ${''}
        ## One
        ### Nested
        ## Two
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'always-one',
      },
    },
    {
      testName: 'Uses a custom bullet marker and indent size',
      before: dedent`
        <!-- toc -->
        ${''}
        ## One
        ### Nested
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        * [One](#one)
            * [Nested](#nested)
        ${''}
        <!-- /toc -->
        ${''}
        ## One
        ### Nested
      `,
      options: {
        bulletMarker: '*',
        indentSize: 4,
      },
    },
    {
      testName: 'Excludes literal headings case-insensitively and regex patterns',
      before: dedent`
        <!-- toc -->
        ${''}
        ## Skip Me
        ## skip me
        ## Draft Section
        ## Keep
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Keep](#keep)
        ${''}
        <!-- /toc -->
        ${''}
        ## Skip Me
        ## skip me
        ## Draft Section
        ## Keep
      `,
      options: {
        excludeHeadings: ['skip me', '/draft/'],
      },
    },
    {
      testName: 'Does not let an explicit id collide without a suffix and ignores headings inside the toc region',
      before: dedent`
        <!-- toc -->
        ## Inside
        <!-- /toc -->
        ${''}
        ## Inside
        ## Other {#inside}
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Inside](#inside)
        - [Other](#inside-1)
        ${''}
        <!-- /toc -->
        ${''}
        ## Inside
        ## Other {#inside}
      `,
      options: {
        useExplicitIds: true,
      },
    },
    {
      testName: 'Adds a blank line after a table of contents at the end of the file',
      before: dedent`
        ## End
        ${''}
        <!--toc-->
      `,
      after: dedent`
        ## End
        ${''}
        <!-- toc -->
        ${''}
        - [End](#end)
        ${''}
        <!-- /toc -->
      ` + '\n\n',
    },
    {
      testName: 'Strips closing heading hashes from the anchor and escapes brackets in the link text',
      before: dedent`
        <!-- toc -->
        ${''}
        ## Hello ##
        ## A]B
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Hello](#hello)
        - [A\\]B](#ab)
        ${''}
        <!-- /toc -->
        ${''}
        ## Hello ##
        ## A]B
      `,
    },
  ],
});

describe('Auto TOC stability', () => {
  const rule = AutoToc.getRule();

  it('does not change a generated table of contents on a second pass', () => {
    const once = rule.apply(dedent`
      # Title
      ${''}
      <!-- toc -->
      ${''}
      ## Alpha
      ### Beta
    `);
    expect(rule.apply(once)).toBe(once);
  });
});
