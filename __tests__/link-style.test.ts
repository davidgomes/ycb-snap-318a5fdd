import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Does nothing when both styles are no-change',
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
      testName: 'Converts wiki links to markdown without changing images when only link style is markdown',
      before: dedent`
        See [[t]] and [[t|d]] plus ![[f.png|300]]
      `,
      after: dedent`
        See [t](t) and [d](t) plus ![[f.png|300]]
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Converts embeds to markdown images without changing wiki links when only image style is markdown',
      before: dedent`
        [[t|d]] ![[f.png]] ![[f.png|300]] ![[f.png|300x200]] ![[f.png|caption]] ![[f.png|caption|300]]
      `,
      after: dedent`
        [[t|d]] ![f.png](f.png) ![f.png](f.png) ![f.png](f.png) ![caption](f.png) ![caption](f.png)
      `,
      options: {
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Uses the default heading display for wiki heading links',
      before: dedent`
        [[p#h]]
        [[#h]]
        [[p#h|custom]]
        [[p#h1#h2]]
        [[#h1#h2]]
      `,
      after: dedent`
        [p > h](p#h)
        [h](#h)
        [custom](p#h)
        [p > h1 > h2](p#h1#h2)
        [h1 > h2](#h1#h2)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Does not treat a numeric wiki link alias as an embed size',
      before: '[[t|300]] and [[t|300x200]]',
      after: '[300](t) and [300x200](t)',
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Leaves unrecognized wiki embeds with a non-size third segment unchanged',
      before: '![[f.png|alt|caption]]',
      after: '![[f.png|alt|caption]]',
      options: {
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Converts inline markdown links to wiki and omits display text that matches the target or heading display',
      before: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        [custom](p#h)
        [p#h](p#h)
        [p > h1 > h2](p#h1#h2)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        [[p#h|custom]]
        [[p#h]]
        [[p#h1#h2]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Converts markdown images to embeds and omits empty or matching alt text',
      before: dedent`
        ![f.png](f.png)
        ![](f.png)
        ![caption](f.png)
        ![p > h](p#h)
      `,
      after: dedent`
        ![[f.png]]
        ![[f.png]]
        ![[f.png|caption]]
        ![[p#h]]
      `,
      options: {
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Never converts external targets',
      before: dedent`
        [d](https://example.com)
        [d](note://open)
        ![alt](https://example.com/a.png)
        [d](<https://example.com/a b>)
        [[https://example.com]]
      `,
      after: dedent`
        [d](https://example.com)
        [d](note://open)
        ![alt](https://example.com/a.png)
        [d](<https://example.com/a b>)
        [[https://example.com]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      // The last line is wiki-to-markdown, so run a dedicated case below for mixed directions.
      testName: 'External markdown stays put while wiki externals can still become markdown',
      before: '[[https://example.com]] and [d](https://example.com)',
      after: '[https://example.com](https://example.com) and [d](https://example.com)',
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Does not convert markdown links or images that include a title',
      before: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        ![alt](f.png "title")
        [d](<My Page> "title")
      `,
      after: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        ![alt](f.png "title")
        [d](<My Page> "title")
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Does not convert single constructs whose label, destination, or title contains a newline',
      before: dedent`
        [hello
        there](target)
        [d](tar
        get)
        [d](t "ti
        tle")
        [keep](page)
      `,
      after: dedent`
        [hello
        there](target)
        [d](tar
        get)
        [d](t "ti
        tle")
        [[page|keep]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Supports nested brackets and backslash escapes in labels',
      before: dedent`
        [a [b] c](t)
        [a \\[b\\]](t)
        [a\\]b](t)
      `,
      after: dedent`
        [[t|a [b] c]]
        [[t|a [b]]]
        [[t|a]b]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Supports angle-bracket destinations, escaped spaces, and balanced parentheses',
      before: dedent`
        [Display]( <My Page> )
        [My Page](<My Page>)
        [d](My\\ Page)
        [My Page](My\\ Page)
        [d](file(1).md)
        [d](foo(bar(baz)))
        [d](foo\\(bar\\))
        [d](a\\<b\\>)
        [file(1).md](file(1).md)
      `,
      after: dedent`
        [[My Page|Display]]
        [[My Page]]
        [[My Page|d]]
        [[My Page]]
        [[file(1).md|d]]
        [[foo(bar(baz))|d]]
        [[foo(bar)|d]]
        [[a<b>|d]]
        [[file(1).md]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Leaves reference links, autolinks, footnotes, and checkboxes unchanged',
      before: dedent`
        [d][ref]
        [d][]
        [id]: page
        <https://example.com>
        [^1]
        - [ ] task [[t]]
      `,
      after: dedent`
        [d][ref]
        [d][]
        [id]: page
        <https://example.com>
        [^1]
        - [ ] task [t](t)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Link style and image style can convert in opposite directions in one pass',
      before: '[[a]] and [b](c) and ![[f.png|300]] and ![alt](g.png)',
      after: '[a](a) and [b](c) and ![f.png](f.png) and ![alt](g.png)',
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki links inside a markdown label are still converted to markdown',
      before: 'See [the [[page]] here](readme)',
      after: 'See [the [page](page) here](readme)',
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'A wiki link followed by parentheses is not parsed as a markdown link',
      before: '[[t]](url)',
      after: '[t](t)(url)',
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Does not convert a wiki link followed by parentheses into a different wiki target',
      before: '[[t]](url)',
      after: '[[t]](url)',
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Block references keep the raw target instead of a heading display',
      before: dedent`
        [[p#^b]]
        [[#^b]]
      `,
      after: dedent`
        [p#^b](p#^b)
        [#^b](#^b)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Markdown block references omit display text only when it equals the target',
      before: dedent`
        [p#^b](p#^b)
        [custom](p#^b)
      `,
      after: dedent`
        [[p#^b]]
        [[p#^b|custom]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Link and image styles can point in opposite directions',
      before: '[[a]] [b](c) ![[f.png|cap]] ![alt](g.png)',
      after: '[[a]] [[c|b]] ![cap](f.png) ![alt](g.png)',
      options: {
        linkStyle: 'wiki',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Inline code inside a wiki target is left in place when converting would duplicate its placeholder',
      before: '[[`code`]]',
      after: '[[`code`]]',
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Inline code in a markdown label is preserved when converting to a wiki link',
      before: '[see `code`](page)',
      after: '[[page|see `code`]]',
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Escaped brackets do not start a link and escaped embeds stay literal',
      before: '\\[[t]] \\![[f.png]] [[real]]',
      after: '\\[[t]] \\![f.png](f.png) [real](real)',
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Does not modify protected regions',
      before: dedent`
        ---
        title: [[Note]]
        ---
        [[Note]]
        \`[[Note]]\`
        \`\`\`
        [[Note]]
        \`\`\`
        $$
        [[Note]]
        $$
        $[[Note]]$
        <% [[Note]] %>
        %% [[Note]] %%
        %%
        [[Note]]
        %%
        | [[Note]] |
        | --- |
        | cell |
        <!-- linter-disable -->
        [[Note]]
        <!-- linter-enable -->
        %% linter-disable %%
        [[Note]]
        %% linter-enable %%
        <div>
        [[Note]]
        </div>

        See <span>[[Note]]</span> here
      `,
      after: dedent`
        ---
        title: [[Note]]
        ---
        [Note](Note)
        \`[[Note]]\`
        \`\`\`
        [[Note]]
        \`\`\`
        $$
        [[Note]]
        $$
        $[[Note]]$
        <% [[Note]] %>
        %% [[Note]] %%
        %%
        [[Note]]
        %%
        | [[Note]] |
        | --- |
        | cell |
        <!-- linter-disable -->
        [[Note]]
        <!-- linter-enable -->
        %% linter-disable %%
        [[Note]]
        %% linter-enable %%
        <div>
        [[Note]]
        </div>

        See <span>[Note](Note)</span> here
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
  ],
});
