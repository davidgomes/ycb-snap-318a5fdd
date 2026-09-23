import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Text without a start marker is left unchanged',
      before: dedent`
        ## Heading 1
        ## Heading 2
      `,
      after: dedent`
        ## Heading 1
        ## Heading 2
      `,
    },
    {
      testName: 'A start marker only in a code block is left unchanged',
      before: dedent`
        \`\`\`
        <!-- toc -->
        \`\`\`
        ## Heading
      `,
      after: dedent`
        \`\`\`
        <!-- toc -->
        \`\`\`
        ## Heading
      `,
    },
    {
      testName: 'Markers are case-insensitive and whitespace-tolerant and the existing TOC is replaced',
      before: dedent`
        <!--TOC-->
        - [Stale](#stale)
        <!--   /Toc   -->
        ## Fresh
      `,
      after: dedent`
        <!--TOC-->
        ${''}
        - [Fresh](#fresh)
        ${''}
        <!--   /Toc   -->
        ${''}
        ## Fresh
      `,
    },
    {
      testName: 'Only the first start marker and the first end marker after it are used',
      before: dedent`
        <!-- /toc -->
        <!-- toc -->
        <!-- /toc -->
        ## A
        <!-- toc -->
        <!-- /toc -->
      `,
      after: dedent`
        <!-- /toc -->
        <!-- toc -->
        ${''}
        - [A](#a)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        <!-- toc -->
        <!-- /toc -->
      `,
    },
    {
      testName: 'Missing end marker is inserted',
      before: dedent`
        <!-- toc -->
        ## A
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [A](#a)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
      `,
    },
    {
      testName: 'Running twice gives the same result',
      before: dedent`
        <!-- toc -->
        ${''}
        - [A](#a)
          - [B](#b)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ### B
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [A](#a)
          - [B](#b)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ### B
      `,
    },
    {
      testName: 'No headings yields an empty TOC region',
      before: dedent`
        <!-- toc -->
        Text
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        <!-- /toc -->
        ${''}
        Text
      `,
    },
    {
      testName: 'End marker at the end of the file does not get trailing blank lines',
      before: dedent`
        ## A
        <!-- toc --><!-- /toc -->
      `,
      after: dedent`
        ## A
        <!-- toc -->
        ${''}
        - [A](#a)
        ${''}
        <!-- /toc -->
      `,
    },
    {
      testName: 'Headings inside the TOC region, YAML, code blocks, and math blocks are ignored',
      before: dedent`
        ---
        key: value
        # comment
        ---
        <!-- toc -->
        ## Inside region
        <!-- /toc -->
        \`\`\`
        ## In code
        \`\`\`
        ${''}
        $$
        ## In math
        $$
        ${''}
        ## Real
        #not-a-heading
        ####### Too deep
      `,
      after: dedent`
        ---
        key: value
        # comment
        ---
        <!-- toc -->
        ${''}
        - [Real](#real)
        ${''}
        <!-- /toc -->
        ${''}
        \`\`\`
        ## In code
        \`\`\`
        ${''}
        $$
        ## In math
        $$
        ${''}
        ## Real
        #not-a-heading
        ####### Too deep
      `,
    },
    {
      testName: 'Anchors resolve links, remove images and formatting, strip trailing hashes, and are deduplicated',
      before: dedent`
        <!-- toc -->
        ## See [the docs](https://example.com) and [[Note|alias]] ##
        ## ![[image.png]] ![alt](pic.png) Pics
        ## **Bold** _italic_ \`code\` ~~strike~~ ==mark==
        ## snake_case & more!!
        ## Pics
        ## Pics
        ## Pics-1
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [See [the docs](https://example.com) and [[Note|alias]]](#see-the-docs-and-alias)
        - [![[image.png]] ![alt](pic.png) Pics](#pics)
        - [**Bold** _italic_ \`code\` ~~strike~~ ==mark==](#bold-italic-code-strike-mark)
        - [snake_case & more!!](#snake_case-more)
        - [Pics](#pics-1)
        - [Pics](#pics-2)
        - [Pics-1](#pics-1-1)
        ${''}
        <!-- /toc -->
        ${''}
        ## See [the docs](https://example.com) and [[Note|alias]] ##
        ## ![[image.png]] ![alt](pic.png) Pics
        ## **Bold** _italic_ \`code\` ~~strike~~ ==mark==
        ## snake_case & more!!
        ## Pics
        ## Pics
        ## Pics-1
      `,
    },
    {
      testName: 'stripFormattingInToc uses the plain text in the TOC',
      before: dedent`
        <!-- toc -->
        ## **Bold** [link](url) ![img](x.png)
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Bold link](#bold-link)
        ${''}
        <!-- /toc -->
        ${''}
        ## **Bold** [link](url) ![img](x.png)
      `,
      options: {
        stripFormattingInToc: true,
      },
    },
    {
      testName: 'Explicit ids are only used when useExplicitIds is enabled',
      before: dedent`
        <!-- toc -->
        ## Heading {#custom-id}
        ## Other {#custom-id}
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Heading {#custom-id}](#heading-custom-id)
        - [Other {#custom-id}](#other-custom-id)
        ${''}
        <!-- /toc -->
        ${''}
        ## Heading {#custom-id}
        ## Other {#custom-id}
      `,
    },
    {
      testName: 'Explicit ids provide the base anchor when useExplicitIds is enabled',
      before: dedent`
        <!-- toc -->
        ## Heading {#custom-id}
        ## Other {#custom-id}
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Heading](#custom-id)
        - [Other](#custom-id-1)
        ${''}
        <!-- /toc -->
        ${''}
        ## Heading {#custom-id}
        ## Other {#custom-id}
      `,
      options: {
        useExplicitIds: true,
      },
    },
    {
      testName: 'Levels are filtered by minLevel and maxLevel with custom indentation and bullet marker',
      before: dedent`
        <!-- toc -->
        # H1
        ## H2
        ### H3
        #### H4
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        * [H1](#h1)
            * [H2](#h2)
                * [H3](#h3)
        ${''}
        <!-- /toc -->
        ${''}
        # H1
        ## H2
        ### H3
        #### H4
      `,
      options: {
        minLevel: 1,
        maxLevel: 3,
        indentSize: 4,
        bulletMarker: '*',
      },
    },
    {
      testName: 'Numbered list with always-one style',
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
      },
    },
    {
      testName: 'Numbered list with increment style increments across all items',
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
          2. [B](#b)
        3. [C](#c)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ### B
        ## C
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'increment',
      },
    },
    {
      testName: 'Title is added with blank lines around it',
      before: dedent`
        Intro
        <!-- toc -->
        <!-- /toc -->
        Body
        ## A
      `,
      after: dedent`
        Intro
        <!-- toc -->
        ${''}
        ## Contents
        ${''}
        - [A](#a)
        ${''}
        <!-- /toc -->
        ${''}
        Body
        ## A
      `,
      options: {
        title: '## Contents',
      },
    },
    {
      testName: 'excludeHeadings matches literals case-insensitively and regexes case-insensitively',
      before: dedent`
        <!-- toc -->
        ## Overview
        ## CHANGELOG
        ## Changelog details
        ## Appendix A
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Overview](#overview)
        - [Changelog details](#changelog-details)
        ${''}
        <!-- /toc -->
        ${''}
        ## Overview
        ## CHANGELOG
        ## Changelog details
        ## Appendix A
      `,
      options: {
        excludeHeadings: ['changelog', '/^APPENDIX/'],
      },
    },
    {
      testName: 'Settings values from the UI are parsed',
      before: dedent`
        <!-- toc -->
        ## A
        ### B
        ## Skip
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [A](#a)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ### B
        ## Skip
      `,
      options: {
        'max-level': '2',
        'exclude-headings': 'skip\n',
      } as any,
    },
  ],
});
