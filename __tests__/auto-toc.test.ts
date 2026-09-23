import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Text is unchanged when the start marker is only present in a code block',
      before: dedent`
        \`\`\`markdown
        <!-- toc -->
        \`\`\`
        ${''}
        ## Heading
      `,
      after: dedent`
        \`\`\`markdown
        <!-- toc -->
        \`\`\`
        ${''}
        ## Heading
      `,
    },
    {
      testName: 'Markers are matched case-insensitively and with any whitespace and are preserved as written',
      before: dedent`
        <!--TOC-->
        stale content
        <!--   /Toc   -->
        ## Heading
      `,
      after: dedent`
        <!--TOC-->
        ${''}
        - [Heading](#heading)
        ${''}
        <!--   /Toc   -->
        ${''}
        ## Heading
      `,
    },
    {
      testName: 'Only the first start marker and the first end marker after it are used',
      before: dedent`
        <!-- /toc -->
        <!-- toc -->
        <!-- /toc -->
        ${''}
        ## One
        ${''}
        <!-- toc -->
        <!-- /toc -->
        ${''}
        ## Two
      `,
      after: dedent`
        <!-- /toc -->
        <!-- toc -->
        ${''}
        - [One](#one)
        - [Two](#two)
        ${''}
        <!-- /toc -->
        ${''}
        ## One
        ${''}
        <!-- toc -->
        <!-- /toc -->
        ${''}
        ## Two
      `,
    },
    {
      testName: 'An end marker is inserted when missing even at the end of the file',
      before: dedent`
        ## Heading
        ${''}
        <!-- toc -->
      `,
      after: dedent`
        ## Heading
        ${''}
        <!-- toc -->
        ${''}
        - [Heading](#heading)
        ${''}
        <!-- /toc -->
      `,
    },
    {
      testName: 'An empty TOC region is kept when there are no headings to include',
      before: dedent`
        <!-- toc -->
        Some text
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        <!-- /toc -->
        ${''}
        Some text
      `,
    },
    {
      testName: 'Headings in YAML, code blocks, math blocks, and the TOC region are ignored while setext headings are not included',
      before: dedent`
        ---
        # yaml comment
        ---
        <!-- toc -->
        ## Old TOC heading
        <!-- /toc -->
        ## Real
        \`\`\`
        ## Fenced
        \`\`\`
        ~~~
        ## Tilde fenced
        ~~~
        $$
        ## Math
        $$
        Setext
        ------
        #hashtag
      `,
      after: dedent`
        ---
        # yaml comment
        ---
        <!-- toc -->
        ${''}
        - [Real](#real)
        ${''}
        <!-- /toc -->
        ${''}
        ## Real
        \`\`\`
        ## Fenced
        \`\`\`
        ~~~
        ## Tilde fenced
        ~~~
        $$
        ## Math
        $$
        Setext
        ------
        #hashtag
      `,
    },
    {
      testName: 'A title that is a heading is added and not included in the TOC on subsequent runs',
      before: dedent`
        <!-- toc -->
        ${''}
        ## Contents
        ${''}
        - [Old](#old)
        ${''}
        <!-- /toc -->
        ${''}
        ## First
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        ## Contents
        ${''}
        - [First](#first)
        ${''}
        <!-- /toc -->
        ${''}
        ## First
      `,
      options: {
        title: '## Contents',
      },
    },
    {
      testName: 'Anchors resolve links, remove image embeds and formatting, and strip trailing heading hashes and special characters',
      before: dedent`
        <!-- toc -->
        ## [[Some Note|Alias]] and [[Other Note]] ##
        ## ![[icon.png]] ![alt](image.png) [Link Text](https://example.com/page_(1))
        ## **Bold** *italic* __strong__ _em_ ~~strike~~ ==mark== \`code\` <b>html</b>
        ## snake_case & C++ -- Done!
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Alias and Other Note](#alias-and-other-note)
        - [Link Text](#link-text)
        - [**Bold** *italic* __strong__ _em_ ~~strike~~ ==mark== \`code\` <b>html</b>](#bold-italic-strong-em-strike-mark-code-html)
        - [snake_case & C++ -- Done!](#snake_case-c-done)
        ${''}
        <!-- /toc -->
        ${''}
        ## [[Some Note|Alias]] and [[Other Note]] ##
        ## ![[icon.png]] ![alt](image.png) [Link Text](https://example.com/page_(1))
        ## **Bold** *italic* __strong__ _em_ ~~strike~~ ==mark== \`code\` <b>html</b>
        ## snake_case & C++ -- Done!
      `,
    },
    {
      testName: 'Formatting is removed from TOC entries when stripFormattingInToc is enabled',
      before: dedent`
        <!-- toc -->
        ## **Bold** *italic* __strong__ _em_ ~~strike~~ ==mark== \`code\` <b>html</b>
        ## ***Both*** and snake_case_name
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Bold italic strong em strike mark code html](#bold-italic-strong-em-strike-mark-code-html)
        - [Both and snake_case_name](#both-and-snake_case_name)
        ${''}
        <!-- /toc -->
        ${''}
        ## **Bold** *italic* __strong__ _em_ ~~strike~~ ==mark== \`code\` <b>html</b>
        ## ***Both*** and snake_case_name
      `,
      options: {
        stripFormattingInToc: true,
      },
    },
    {
      testName: 'Duplicate anchors get incrementing suffixes without colliding with existing anchors',
      before: dedent`
        <!-- toc -->
        ## Intro
        ## Intro
        ## Intro 1
        ## Intro
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Intro](#intro)
        - [Intro](#intro-1)
        - [Intro 1](#intro-1-1)
        - [Intro](#intro-2)
        ${''}
        <!-- /toc -->
        ${''}
        ## Intro
        ## Intro
        ## Intro 1
        ## Intro
      `,
    },
    {
      testName: 'Explicit IDs are treated as heading text when useExplicitIds is disabled',
      before: dedent`
        <!-- toc -->
        ## Heading {#custom}
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Heading {#custom}](#heading-custom)
        ${''}
        <!-- /toc -->
        ${''}
        ## Heading {#custom}
      `,
    },
    {
      testName: 'Explicit IDs provide the base anchor and are deduplicated when useExplicitIds is enabled',
      before: dedent`
        <!-- toc -->
        ## First {#same}
        ## Second {#same} ##
        ## Third
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [First](#same)
        - [Second](#same-1)
        - [Third](#third)
        ${''}
        <!-- /toc -->
        ${''}
        ## First {#same}
        ## Second {#same} ##
        ## Third
      `,
      options: {
        useExplicitIds: true,
      },
    },
    {
      testName: 'Headings are excluded by case-insensitive literals and case-insensitive regexes',
      before: dedent`
        <!-- toc -->
        ## Keep
        ## CHANGELOG
        ## Changelog Archive
        ## Appendix A
        ## **Notes**
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Keep](#keep)
        - [Changelog Archive](#changelog-archive)
        ${''}
        <!-- /toc -->
        ${''}
        ## Keep
        ## CHANGELOG
        ## Changelog Archive
        ## Appendix A
        ## **Notes**
      `,
      options: {
        excludeHeadings: ['changelog', '/^APPENDIX/', 'notes'],
      },
    },
    {
      testName: 'Heading levels outside of minLevel and maxLevel are left out and indentation is relative to the shallowest included heading',
      before: dedent`
        <!-- toc -->
        # Title
        ## Section
        ### Subsection
        #### Deep
        ### Other Subsection
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        - [Subsection](#subsection)
          - [Deep](#deep)
        - [Other Subsection](#other-subsection)
        ${''}
        <!-- /toc -->
        ${''}
        # Title
        ## Section
        ### Subsection
        #### Deep
        ### Other Subsection
      `,
      options: {
        minLevel: 3,
        maxLevel: 4,
      },
    },
    {
      testName: 'Numbered lists use 1. for every item by default',
      before: dedent`
        <!-- toc -->
        ## One
        ### One A
        ## Two
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        1. [One](#one)
          1. [One A](#one-a)
        1. [Two](#two)
        ${''}
        <!-- /toc -->
        ${''}
        ## One
        ### One A
        ## Two
      `,
      options: {
        listStyle: 'number',
      },
    },
    {
      testName: 'Numbered lists increment across all items when orderedListStyle is increment',
      before: dedent`
        <!-- toc -->
        ## One
        ### One A
        ## Two
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        1. [One](#one)
            2. [One A](#one-a)
        3. [Two](#two)
        ${''}
        <!-- /toc -->
        ${''}
        ## One
        ### One A
        ## Two
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'increment',
        indentSize: 4,
      },
    },
    {
      testName: 'Running the rule on its own output does not change it',
      before: dedent`
        <!-- toc -->
        ${''}
        **Contents**
        ${''}
        - [A](#a)
          - [B](#b)
        ${''}
        <!-- /toc -->
        ${''}
        ${''}
        ## A
        ### B
      `,
      after: dedent`
        <!-- toc -->
        ${''}
        **Contents**
        ${''}
        - [A](#a)
          - [B](#b)
        ${''}
        <!-- /toc -->
        ${''}
        ${''}
        ## A
        ### B
      `,
      options: {
        title: '**Contents**',
      },
    },
  ],
});

describe('Auto TOC settings values', () => {
  it('Numeric options provided as strings from the settings are respected', () => {
    const before = dedent`
      <!-- toc -->
      # Title
      ## Section
    `;
    const after = dedent`
      <!-- toc -->
      ${''}
      * [Title](#title)
          * [Section](#section)
      ${''}
      <!-- /toc -->
      ${''}
      # Title
      ## Section
    `;

    expect(AutoToc.getRule().apply(before, {'min-level': '1', 'indent-size': '4', 'bullet-marker': '*'})).toBe(after);
  });
});
