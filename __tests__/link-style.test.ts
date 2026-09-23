import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Default options leave links and images unchanged',
      before: dedent`
        [[Page]] [Page](Page) ![[image.png]] ![alt](image.png)
      `,
      after: dedent`
        [[Page]] [Page](Page) ![[image.png]] ![alt](image.png)
      `,
    },
    {
      testName: 'Wiki links are converted to markdown links',
      before: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[p#h|d]]
        [[My Page]]
        [[a(b)]]
      `,
      after: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [d](p#h)
        [My Page](<My Page>)
        [a(b)](a(b))
      `,
      options: {linkStyle: 'markdown'},
    },
    {
      testName: 'Wiki embeds are converted to markdown images and size displays are dropped',
      before: dedent`
        ![[f.png]]
        ![[f.png|300]]
        ![[f.png|300x200]]
        ![[f.png|alt]]
        ![[My Image.png]]
      `,
      after: dedent`
        ![f.png](f.png)
        ![f.png](f.png)
        ![f.png](f.png)
        ![alt](f.png)
        ![My Image.png](<My Image.png>)
      `,
      options: {imageStyle: 'markdown'},
    },
    {
      testName: 'Embed display of 300 is kept for regular wiki links',
      before: dedent`
        [[t|300]]
      `,
      after: dedent`
        [300](t)
      `,
      options: {linkStyle: 'markdown'},
    },
    {
      testName: 'Link style only affects links and image style only affects images',
      before: dedent`
        [[t]] ![[f.png]] [d](t) ![alt](f.png)
      `,
      after: dedent`
        [t](t) ![[f.png]] [d](t) ![[f.png|alt]]
      `,
      options: {linkStyle: 'markdown', imageStyle: 'wiki'},
    },
    {
      testName: 'Markdown links are converted to wiki links',
      before: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [d](p#h)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[p#h|d]]
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Markdown images are converted to wiki embeds',
      before: dedent`
        ![alt](f.png)
        ![](f.png)
        ![f.png](f.png)
      `,
      after: dedent`
        ![[f.png|alt]]
        ![[f.png]]
        ![[f.png]]
      `,
      options: {imageStyle: 'wiki'},
    },
    {
      testName: 'External targets are never converted to wiki links',
      before: dedent`
        [Example](https://example.com)
        ![alt](https://example.com/f.png)
        [Local](obsidian://open?vault=x)
      `,
      after: dedent`
        [Example](https://example.com)
        ![alt](https://example.com/f.png)
        [Local](obsidian://open?vault=x)
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Markdown links with titles are not converted',
      before: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        ![alt](f.png "title")
      `,
      after: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        ![alt](f.png "title")
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Markdown links spanning multiple lines are not converted',
      before: dedent`
        [multi
        line](t)
        [d](
        t)
        [d](t
        )
        [d](t
        "title")
      `,
      after: dedent`
        [multi
        line](t)
        [d](
        t)
        [d](t
        )
        [d](t
        "title")
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Nested brackets and backslash escapes in labels are supported',
      before: dedent`
        [a [b] c](t)
        [a \\* b](t)
        [t\\_x](t_x)
      `,
      after: dedent`
        [[t|a [b] c]]
        [[t|a * b]]
        [[t_x]]
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Angle bracket destinations with optional surrounding whitespace are supported',
      before: dedent`
        [My Page](<My Page>)
        [d](<My Page>)
        [d]( <My Page> )
        ![alt]( <My Image.png> )
        [d](<a\\>b>)
      `,
      after: dedent`
        [[My Page]]
        [[My Page|d]]
        [[My Page|d]]
        ![[My Image.png|alt]]
        [[a>b|d]]
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Destinations with balanced parentheses and backslash escapes are supported',
      before: dedent`
        [d](a(b)c)
        [d](a\\(b)
        [d](a\\)b)
        [d](My\\ Page)
        [d](a\\<b\\>)
      `,
      after: dedent`
        [[a(b)c|d]]
        [[a(b|d]]
        [[a)b|d]]
        [[My Page|d]]
        [[a<b>|d]]
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Unbalanced parentheses in a destination leave the link unchanged',
      before: dedent`
        [d](a(b)
      `,
      after: dedent`
        [d](a(b)
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Links that are not inline links are left alone',
      before: dedent`
        [d][ref]
        [d]
        \\[d](t)
        \\![alt](f.png)
        [](t)
        [d]()
        [d](a b)

        [ref]: t
      `,
      after: dedent`
        [d][ref]
        [d]
        \\[d](t)
        \\![alt](f.png)
        [](t)
        [d]()
        [d](a b)

        [ref]: t
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Escaped wiki links are left alone',
      before: dedent`
        \\[[t]] \\![[f.png]]
      `,
      after: dedent`
        \\[[t]] \\![[f.png]]
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
    {
      testName: 'Images inside of markdown links are left alone',
      before: dedent`
        [![badge](badge.png)](https://example.com)
        [![alt](f.png)](t)
      `,
      after: dedent`
        [![badge](badge.png)](https://example.com)
        [![alt](f.png)](t)
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Links and embeds in do-not-modify regions are left alone when converting to markdown',
      before: dedent`
        ---
        link: "[[t]]"
        ---
        ${''}
        \`\`\`
        [[t]]
        \`\`\`
        ${''}
        \`[[t]]\` and $[[t]]$
        ${''}
        $$
        [[t]]
        $$
        ${''}
        <div>
        [[t]]
        </div>
        ${''}
        <% [[t]] %>
        ${''}
        %% [[t]] %%
        ${''}
        %%
        [[t]]
        %%
        ${''}
        | Header |
        | ------ |
        | [[t]] |
        ${''}
        <!-- linter-disable -->
        [[t]] ![[f.png]]
        <!-- linter-enable -->
        ${''}
        [[t]] ![[f.png]]
      `,
      after: dedent`
        ---
        link: "[[t]]"
        ---
        ${''}
        \`\`\`
        [[t]]
        \`\`\`
        ${''}
        \`[[t]]\` and $[[t]]$
        ${''}
        $$
        [[t]]
        $$
        ${''}
        <div>
        [[t]]
        </div>
        ${''}
        <% [[t]] %>
        ${''}
        %% [[t]] %%
        ${''}
        %%
        [[t]]
        %%
        ${''}
        | Header |
        | ------ |
        | [[t]] |
        ${''}
        <!-- linter-disable -->
        [[t]] ![[f.png]]
        <!-- linter-enable -->
        ${''}
        [t](t) ![f.png](f.png)
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
    {
      testName: 'Links and images in do-not-modify regions are left alone when converting to wiki',
      before: dedent`
        ---
        link: "[d](t)"
        ---
        ${''}
        \`\`\`
        [d](t)
        \`\`\`
        ${''}
        \`[d](t)\` and $[d](t)$
        ${''}
        $$
        [d](t)
        $$
        ${''}
        <div>
        [d](t)
        </div>
        ${''}
        <% [d](t) %>
        ${''}
        %% [d](t) %%
        ${''}
        | Header |
        | ------ |
        | [d](t) |
        ${''}
        <!-- linter-disable -->
        [d](t) ![alt](f.png)
        <!-- linter-enable -->
        ${''}
        [d](t) ![alt](f.png)
      `,
      after: dedent`
        ---
        link: "[d](t)"
        ---
        ${''}
        \`\`\`
        [d](t)
        \`\`\`
        ${''}
        \`[d](t)\` and $[d](t)$
        ${''}
        $$
        [d](t)
        $$
        ${''}
        <div>
        [d](t)
        </div>
        ${''}
        <% [d](t) %>
        ${''}
        %% [d](t) %%
        ${''}
        | Header |
        | ------ |
        | [d](t) |
        ${''}
        <!-- linter-disable -->
        [d](t) ![alt](f.png)
        <!-- linter-enable -->
        ${''}
        [[t|d]] ![[f.png|alt]]
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Markdown generated from wiki links converts back to the original wiki links',
      before: dedent`
        [t](t) [d](t) [p > h](p#h) [h](#h) [My Page](<My Page>) [a(b](<a(b>) ![f.png](f.png) ![alt](f.png) ![My Image.png](<My Image.png>)
      `,
      after: dedent`
        [[t]] [[t|d]] [[p#h]] [[#h]] [[My Page]] [[a(b]] ![[f.png]] ![[f.png|alt]] ![[My Image.png]]
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Wiki links with unbalanced parentheses use angle bracket destinations',
      before: dedent`
        [[a(b]]
      `,
      after: dedent`
        [a(b](<a(b>)
      `,
      options: {linkStyle: 'markdown'},
    },
    {
      testName: 'Links inside of list items and blockquotes are converted',
      before: dedent`
        - [[t]]
        > [[t|d]]
      `,
      after: dedent`
        - [t](t)
        > [d](t)
      `,
      options: {linkStyle: 'markdown'},
    },
  ],
});
