import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Text without a toc marker is unchanged',
      before: dedent`
        ## Heading
        ${''}
        Text
      `,
      after: dedent`
        ## Heading
        ${''}
        Text
      `,
    },
    {
      testName: 'Markers are case-insensitive and whitespace-tolerant and the existing TOC is replaced',
      before: dedent`
        <!--TOC-->
        - [Stale](#stale)
        <!--   /Toc   -->
        Text
        ## A
      `,
      after: dedent`
        <!--TOC-->
        ${''}
        - [A](#a)
        ${''}
        <!--   /Toc   -->
        ${''}
        Text
        ## A
      `,
    },
    {
      testName: 'Headings in YAML, code blocks, math blocks, and the TOC region are ignored',
      before: dedent`
        ---
        key: value
        ---
        <!-- toc -->
        ## Inside
        <!-- /toc -->
        ${''}
        \`\`\`
        ## Code
        \`\`\`
        ${''}
        $$
        ## Math
        $$
        ${''}
        ## Real
      `,
      after: dedent`
        ---
        key: value
        ---
        <!-- toc -->
        ${''}
        - [Real](#real)
        ${''}
        <!-- /toc -->
        ${''}
        \`\`\`
        ## Code
        \`\`\`
        ${''}
        $$
        ## Math
        $$
        ${''}
        ## Real
      `,
    },
    {
      testName: 'Anchors resolve links, remove images and formatting, strip closing hashes, and deduplicate',
      before: dedent`
        <!-- toc -->
        ## Hello **World** ##
        ## [[Page|Alias]] and [link](http://x.com) ![[img.png]] ![alt](a.png)
        ## Hello World
        ## What's  new?!
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Hello **World**](#hello-world)
        - [[[Page|Alias]] and [link](http://x.com) ![[img.png]] ![alt](a.png)](#alias-and-link)
        - [Hello World](#hello-world-1)
        - [What's  new?!](#whats-new)
        ${''}
        <!-- /toc -->
        ${''}
        ## Hello **World** ##
        ## [[Page|Alias]] and [link](http://x.com) ![[img.png]] ![alt](a.png)
        ## Hello World
        ## What's  new?!
      `,
    },
    {
      testName: 'Numbered lists with increment, custom indent, title, levels, explicit ids, stripped formatting, and exclusions',
      before: dedent`
        # Top
        <!-- toc -->
        ## First {#custom}
        ### *Sub*
        #### Too deep
        ## Skip Me
        ## Changelog 2024
      `,
      after: dedent`
        # Top
        <!-- toc -->
        ${''}
        **Contents**
        ${''}
        1. [First](#custom)
            2. [Sub](#sub)
        ${''}
        <!-- /toc -->
        ${''}
        ## First {#custom}
        ### *Sub*
        #### Too deep
        ## Skip Me
        ## Changelog 2024
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'increment',
        indentSize: 4,
        maxLevel: 3,
        title: '**Contents**',
        useExplicitIds: true,
        stripFormattingInToc: true,
        excludeHeadings: ['skip me', '/^changelog/'],
      },
    },
    {
      testName: 'Bullet marker option and always-one numbering',
      before: dedent`
        <!-- toc -->
        <!-- /toc -->
        ## A
        ## B
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        * [A](#a)
        * [B](#b)
        ${''}
        <!-- /toc -->
        ${''}
        ## A
        ## B
      `,
      options: {
        bulletMarker: '*',
      },
    },
  ],
});
