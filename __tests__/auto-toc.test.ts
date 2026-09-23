import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Leaves text unchanged when the TOC start marker is absent',
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
      testName: 'Inserts a closing marker and bullet list for ATX headings in the default level range',
      before: dedent`
        # Title
        ${''}
        <!-- toc -->
        ${''}
        ## Section
        ${''}
        ### Detail
      `,
      after: dedent`
        # Title
        ${''}
        <!-- toc -->
        ${''}
        - [Section](#section)
          - [Detail](#detail)
        ${''}
        <!-- /toc -->
        ${''}
        ## Section
        ${''}
        ### Detail
      `,
    },
    {
      testName: 'Updates the first TOC region, preserves marker text, and skips headings inside that region',
      before: dedent`
        <!-- TOC -->
        ${''}
        - [Old](#old)
        ${''}
        ## Inside the old toc
        ${''}
        <!-- /TOC -->
        ${''}
        ## Kept
        ${''}
        <!-- toc -->
        stale
        <!-- /toc -->
      `,
      after: dedent`
        <!-- TOC -->
        ${''}
        - [Kept](#kept)
        ${''}
        <!-- /TOC -->
        ${''}
        ## Kept
        ${''}
        <!-- toc -->
        stale
        <!-- /toc -->
      `,
    },
    {
      testName: 'Accepts whitespace and case differences in the markers',
      before: dedent`
        <!--   ToC   -->
        old
        <!--   /   toc   -->
        ${''}
        ## Hello World
      `,
      after: dedent`
        <!--   ToC   -->
        ${''}
        - [Hello World](#hello-world)
        ${''}
        <!--   /   toc   -->
        ${''}
        ## Hello World
      `,
    },
    {
      testName: 'Builds anchors from link display text, without images or formatting, and dedupes them',
      before: dedent`
        <!-- toc -->
        ## Read [the Docs](https://example.com)
        ## [[Page|Other Name]]
        ## Cat ![[cat.png]] Photo
        ## Cat Photo
        ## **Bold** and _italic_ snake_case
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Read [the Docs](https://example.com)](#read-the-docs)
        - [[[Page|Other Name]]](#other-name)
        - [Cat ![[cat.png]] Photo](#cat-photo)
        - [Cat Photo](#cat-photo-1)
        - [**Bold** and _italic_ snake_case](#bold-and-italic-snake_case)
        ${''}
        <!-- /toc -->
        ${''}
        ## Read [the Docs](https://example.com)
        ## [[Page|Other Name]]
        ## Cat ![[cat.png]] Photo
        ## Cat Photo
        ## **Bold** and _italic_ snake_case
      `,
    },
    {
      testName: 'Strips formatting in labels, uses explicit ids, and applies a title when those options are set',
      before: dedent`
        <!-- toc -->
        ## **Bold** {#Custom_Id}
        ## [**Click**](https://example.com) ![x](y.png)
        ## Same
        ## Same
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        Contents
        ${''}
        - [Bold](#Custom_Id)
        - [Click](#click)
        - [Same](#same)
        - [Same](#same-1)
        ${''}
        <!-- /toc -->
        ${''}
        ## **Bold** {#Custom_Id}
        ## [**Click**](https://example.com) ![x](y.png)
        ## Same
        ## Same
      `,
      options: {
        title: 'Contents',
        useExplicitIds: true,
        stripFormattingInToc: true,
      },
    },
    {
      testName: 'Excludes literal and regex headings and can render an incremented ordered list',
      before: dedent`
        <!-- toc -->
        ## Draft
        ## Keep me
        ### Nested
        ## Notes
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        1. [Keep me](#keep-me)
          2. [Nested](#nested)
        ${''}
        <!-- /toc -->
        ${''}
        ## Draft
        ## Keep me
        ### Nested
        ## Notes
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'increment',
        excludeHeadings: ['draft', '/note/'],
      },
    },
    {
      testName: 'Uses the bullet marker, indent size, level range, and always-one ordered markers',
      before: dedent`
        <!-- toc -->
        # Top
        ## A
        ### B
        #### C
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        * [Top](#top)
            * [A](#a)
        ${''}
        <!-- /toc -->
        ${''}
        # Top
        ## A
        ### B
        #### C
      `,
      options: {
        listStyle: 'bullet',
        bulletMarker: '*',
        indentSize: 4,
        minLevel: 1,
        maxLevel: 2,
      },
    },
    {
      testName: 'Ignores ATX-looking lines in YAML, code blocks, and math blocks',
      before: dedent`
        ---
        # not-a-heading
        title: Example
        ---
        ${''}
        \`\`\`
        # not-a-heading
        \`\`\`
        ${''}
        $$
        # not-a-heading
        $$
        ${''}
        <!-- toc -->
        ${''}
        ## Real
      `,
      after: dedent`
        ---
        # not-a-heading
        title: Example
        ---
        ${''}
        \`\`\`
        # not-a-heading
        \`\`\`
        ${''}
        $$
        # not-a-heading
        $$
        ${''}
        <!-- toc -->
        ${''}
        - [Real](#real)
        ${''}
        <!-- /toc -->
        ${''}
        ## Real
      `,
    },
    {
      testName: 'Does not treat a marker that only appears in a code block as an opt-in',
      before: dedent`
        \`\`\`
        <!-- toc -->
        # Heading
        \`\`\`
        ${''}
        ## Outside
      `,
      after: dedent`
        \`\`\`
        <!-- toc -->
        # Heading
        \`\`\`
        ${''}
        ## Outside
      `,
    },
    {
      testName: 'Numbered lists can keep every marker at 1',
      before: dedent`
        <!-- toc -->
        ## A
        ### B
        ## C
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        1. [A](#a)
          1. [B](#b)
        1. [C](#c)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ### B
        ## C
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'always-one',
      },
    },
    {
      testName: 'Drops characters outside a-z, 0-9, hyphen, and underscore, then trims extra hyphens',
      before: dedent`
        <!-- toc -->
        ## --Café & Tea!--
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [--Café & Tea!--](#caf-tea)
        ${''}
        <!-- /toc -->
        ${''}
        ## --Café & Tea!--
      `,
    },
  ],
});

describe('Auto TOC idempotency', () => {
  const rule = AutoToc.getRule();

  it('keeps an already generated TOC stable', () => {
    const before = dedent`
      <!-- toc -->
      ${''}
      ## Section
    `;
    const once = rule.apply(before);
    expect(rule.apply(once)).toBe(once);
  });
});
