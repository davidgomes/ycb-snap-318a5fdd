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
        [[t|d]]
        ![[f.png|300]]
        [d](t)
        ![alt](f.png)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        ![[f.png|300]]
        [d](t)
        ![alt](f.png)
      `,
    },
    {
      testName: 'Link style does not convert embeds, and image style does not convert links',
      before: dedent`
        [[t]]
        ![[f.png]]
        [d](t)
        ![alt](f.png)
      `,
      after: dedent`
        [t](t)
        ![[f.png]]
        [d](t)
        ![[f.png|alt]]
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Heading display is omitted when it matches the default, including same-note headings',
      before: dedent`
        [My Page > My Heading](My Page#My Heading)
        [My Heading](#My Heading)
        [custom](My Page#My Heading)
        [[note#^block]]
      `,
      after: dedent`
        [[My Page#My Heading]]
        [[#My Heading]]
        [[My Page#My Heading|custom]]
        [[note#^block]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown parsing keeps nested labels, balanced destinations, angle destinations, and literal escapes',
      before: dedent`
        [see [this]](page)
        [a \\[b\\]](target)
        [d](foo(bar))
        [d](a\\(b\\))
        [My Page]( <My Page> )
        [My Page](My\\ Page)
        [hello world](hello\\ world)
        [d](<foo\\>bar>)
        [file(1)](file(1))
        [note](file (1).md)
        ![shot](file (1).png)
      `,
      after: dedent`
        [[page|see [this]]]
        [[target|a [b]]]
        [[foo(bar)|d]]
        [[a(b)|d]]
        [[My Page]]
        [[My Page]]
        [[hello world]]
        [[foo>bar|d]]
        [[file(1)]]
        [[file (1).md|note]]
        ![[file (1).png|shot]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'External targets, titles, multiline links, and non-inline syntax stay unchanged',
      before: dedent`
        [d](https://example.com/a)
        ![alt](https://example.com/a.png)
        [d](<https://example.com>)
        [d](note://x)
        [d](t "title")
        [d](t 'title')
        [d](t "")
        [d][ref]
        [shortcut]
        \\[d](t)
        [label](dest
        )
        [label
        ](target)
        [d](t "title
        ")
      `,
      after: dedent`
        [d](https://example.com/a)
        ![alt](https://example.com/a.png)
        [d](<https://example.com>)
        [d](note://x)
        [d](t "title")
        [d](t 'title')
        [d](t "")
        [d][ref]
        [shortcut]
        \\[d](t)
        [label](dest
        )
        [label
        ](target)
        [d](t "title
        ")
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Ignored regions are not converted',
      before: dedent`
        ---
        link: "[[t]]"
        ---
        [[body]]
        \`[[code]]\`
        \`\`\`
        [[fence]]
        [d](t)
        \`\`\`
        $[[math]]$
        $$
        [[blockmath]]
        $$
        <div>
        [[html]]
        </div>
        <% [[templater]] %>
        %% [[comment]] %%
        <!-- linter-disable -->
        [[ignored]]
        <!-- linter-enable -->
        | [[table]] |
        | --- |
        | [d](t) |
      `,
      after: dedent`
        ---
        link: "[[t]]"
        ---
        [body](body)
        \`[[code]]\`
        \`\`\`
        [[fence]]
        [d](t)
        \`\`\`
        $[[math]]$
        $$
        [[blockmath]]
        $$
        <div>
        [[html]]
        </div>
        <% [[templater]] %>
        %% [[comment]] %%
        <!-- linter-disable -->
        [[ignored]]
        <!-- linter-enable -->
        | [[table]] |
        | --- |
        | [d](t) |
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Empty image alt is omitted and a second pass is stable',
      before: dedent`
        ![](f.png)
        ![f.png](f.png)
        ![caption](f.png)
        [t](t)
      `,
      after: dedent`
        ![[f.png]]
        ![[f.png]]
        ![[f.png|caption]]
        [[t]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
  ],
});
