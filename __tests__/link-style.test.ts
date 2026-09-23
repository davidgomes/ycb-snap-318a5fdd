import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Defaults leave wiki and markdown links unchanged',
      before: dedent`
        [[t]]
        [d](t)
        ![[f.png]]
        ![alt](f.png)
      `,
      after: dedent`
        [[t]]
        [d](t)
        ![[f.png]]
        ![alt](f.png)
      `,
    },
    {
      testName: 'Wiki links become markdown links, including heading display',
      before: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        See [[p#h|custom]] here.
      `,
      after: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        See [custom](p#h) here.
      `,
      options: {linkStyle: 'markdown'},
    },
    {
      testName: 'Wiki embeds become markdown images and drop size displays',
      before: dedent`
        ![[f.png]]
        ![[f.png|300]]
        ![[f.png|300x200]]
        ![[f.png|alt]]
        ![[f.png|alt|640]]
      `,
      after: dedent`
        ![f.png](f.png)
        ![f.png](f.png)
        ![f.png](f.png)
        ![alt](f.png)
        ![alt](f.png)
      `,
      options: {imageStyle: 'markdown'},
    },
    {
      testName: 'Markdown links become wiki links and keep external, titled, and multiline links',
      before: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [other](p#h)
        [site](https://example.com)
        [d](t "title")
        [multi
        line](t)
        [a[b]c](target)
        [page]( <My Page> )
        [name](file\\(1\\).md)
        [spaced](My\\ Page)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[p#h|other]]
        [site](https://example.com)
        [d](t "title")
        [multi
        line](t)
        [[target|a[b]c]]
        [[My Page|page]]
        [[file(1).md|name]]
        [[My Page|spaced]]
      `,
      options: {linkStyle: 'wiki'},
    },
    {
      testName: 'Markdown images become wiki embeds',
      before: dedent`
        ![alt](f.png)
        ![f.png](f.png)
        ![](f.png)
        ![remote](https://example.com/a.png)
        ![x](f.png "title")
      `,
      after: dedent`
        ![[f.png|alt]]
        ![[f.png]]
        ![[f.png]]
        ![remote](https://example.com/a.png)
        ![x](f.png "title")
      `,
      options: {imageStyle: 'wiki'},
    },
    {
      testName: 'Ignored regions are not converted',
      before: dedent`
        ---
        alias: [[t]]
        ---
        \`[[t]]\` and \`[d](t)\`
        %%
        [[t]]
        %%
        %%[[t]]%%
        <% [[t]] %>
        <!-- linter-disable -->
        [[t]]
        <!-- linter-enable -->
        | [[t]] |
        | --- |
        | cell |
      `,
      after: dedent`
        ---
        alias: [[t]]
        ---
        \`[[t]]\` and \`[d](t)\`
        %%
        [[t]]
        %%
        %%[[t]]%%
        <% [[t]] %>
        <!-- linter-disable -->
        [[t]]
        <!-- linter-enable -->
        | [[t]] |
        | --- |
        | cell |
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
  ],
});
