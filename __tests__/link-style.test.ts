import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Default options leave links unchanged',
      before: dedent`
        [[t]] [d](t) ![[f.png]] ![a](f.png)
      `,
      after: dedent`
        [[t]] [d](t) ![[f.png]] ![a](f.png)
      `,
    },
    {
      testName: 'Wiki links and embeds convert to markdown',
      before: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[My Page]]
        ![[f.png]]
        ![[f.png|300]]
        ![[f.png|300x200]]
        ![[f.png|alt]]
      `,
      after: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [My Page](<My Page>)
        ![f.png](f.png)
        ![f.png](f.png)
        ![f.png](f.png)
        ![alt](f.png)
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
    {
      testName: 'Only links convert when image style is no-change',
      before: dedent`
        [[t]] ![[f.png]]
      `,
      after: dedent`
        [t](t) ![[f.png]]
      `,
      options: {linkStyle: 'markdown'},
    },
    {
      testName: 'Markdown links and images convert to wiki',
      before: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [d](<My Page>)
        [d]( <My Page> )
        [d](a(b)c)
        [d](a\\(b)
        [d](My\\ Page)
        [d](a\\<b\\>)
        [a [b] c](t)
        [a \\] b](t)
        ![f.png](f.png)
        ![](f.png)
        ![alt](f.png)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[My Page|d]]
        [[My Page|d]]
        [[a(b)c|d]]
        [[a(b|d]]
        [[My Page|d]]
        [[a<b>|d]]
        [[t|a [b] c]]
        [[t|a ] b]]
        ![[f.png]]
        ![[f.png]]
        ![[f.png|alt]]
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Markdown to wiki leaves unsupported forms unchanged',
      before: dedent`
        [d](https://example.com)
        ![a](http://example.com/f.png)
        [d](t "title")
        [d](t 'title')
        [d
        e](t)
        [d](
        t)
        [d](a(b)
        [d][ref]
        [d](a b)
      `,
      after: dedent`
        [d](https://example.com)
        ![a](http://example.com/f.png)
        [d](t "title")
        [d](t 'title')
        [d
        e](t)
        [d](
        t)
        [d](a(b)
        [d][ref]
        [d](a b)
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
    {
      testName: 'Images are not converted as links when only link style is wiki',
      before: dedent`
        ![a](f.png) [d](t)
      `,
      after: dedent`
        ![a](f.png) [[t|d]]
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Do-not-modify regions are left alone',
      before: dedent`
        ---
        link: "[[t]]"
        ---
        ${''}
        \`[[t]]\`
        ${''}
        \`\`\`
        [[t]]
        \`\`\`
        ${''}
        $[[t]]$
        ${''}
        $$
        [[t]]
        $$
        ${''}
        <div>[[t]]</div>
        ${''}
        <% [[t]] %>
        ${''}
        %% [[t]] %%
        ${''}
        | a | b |
        | - | - |
        | [[t]] | c |
        ${''}
        <!-- linter-disable -->
        [[t]]
        <!-- linter-enable -->
        ${''}
        [[t]]
      `,
      after: dedent`
        ---
        link: "[[t]]"
        ---
        ${''}
        \`[[t]]\`
        ${''}
        \`\`\`
        [[t]]
        \`\`\`
        ${''}
        $[[t]]$
        ${''}
        $$
        [[t]]
        $$
        ${''}
        <div>[[t]]</div>
        ${''}
        <% [[t]] %>
        ${''}
        %% [[t]] %%
        ${''}
        | a | b |
        | - | - |
        | [[t]] | c |
        ${''}
        <!-- linter-disable -->
        [[t]]
        <!-- linter-enable -->
        ${''}
        [t](t)
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
    {
      testName: 'Markdown links in do-not-modify regions are left alone',
      before: dedent`
        \`[d](t)\`
        %% [d](t) %%
        <% [d](t) %>
        [d](t)
      `,
      after: dedent`
        \`[d](t)\`
        %% [d](t) %%
        <% [d](t) %>
        [[t|d]]
      `,
      options: {linkStyle: 'wiki'},
    },
  ],
});
