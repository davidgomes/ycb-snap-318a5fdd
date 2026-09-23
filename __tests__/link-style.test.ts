import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Nothing is changed when both styles are set to no change',
      before: dedent`
        [[Note]] [[Note|Display]] ![[image.png]]
        [Display](Note) ![Alt](image.png)
      `,
      after: dedent`
        [[Note]] [[Note|Display]] ![[image.png]]
        [Display](Note) ![Alt](image.png)
      `,
    },
    // wiki to markdown
    {
      testName: 'Wiki links are converted to markdown links',
      before: dedent`
        [[t]]
        [[t|d]]
        Some text [[First]] and [[Second|second link]] on one line.
      `,
      after: dedent`
        [t](t)
        [d](t)
        Some text [First](First) and [second link](Second) on one line.
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki links to headings use the default heading display text',
      before: dedent`
        [[p#h]]
        [[#h]]
        [[p#h1#h2]]
        [[p#^block-id]]
        [[p#h|d]]
      `,
      after: dedent`
        [p > h](p#h)
        [h](#h)
        [p > h1 > h2](p#h1#h2)
        [p > ^block-id](p#^block-id)
        [d](p#h)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki link targets that are not valid bare markdown destinations are wrapped in angle brackets',
      before: dedent`
        [[My Page]]
        [[My Page#My Heading|d]]
        [[Page (draft)]]
        [[a(b]]
        [[a(b)c]]
      `,
      after: dedent`
        [My Page](<My Page>)
        [d](<My Page#My Heading>)
        [Page (draft)](<Page (draft)>)
        [a(b](<a(b>)
        [a(b)c](a(b)c)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki embeds are left alone when only the link style is set',
      before: dedent`
        [[t]] ![[f.png]] ![[Note]]
      `,
      after: dedent`
        [t](t) ![[f.png]] ![[Note]]
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki embeds are converted to markdown images with size display text dropped',
      before: dedent`
        ![[f.png]]
        ![[f.png|300]]
        ![[f.png|300x200]]
        ![[f.png|alt]]
        ![[My Image.png]]
        ![[Note#Heading]]
        [[t]]
      `,
      after: dedent`
        ![f.png](f.png)
        ![f.png](f.png)
        ![f.png](f.png)
        ![alt](f.png)
        ![My Image.png](<My Image.png>)
        ![Note > Heading](Note#Heading)
        [[t]]
      `,
      options: {
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Wiki links that do not have a clear markdown equivalent are left alone',
      before: dedent`
        [[t|]]
        [[]]
        [[#]]
        [[ t ]]
        [[t|a|b]]
        ![[f.png|alt|300]]
        [[https://example.com]]
        [[a]b]]
        \\[[t]]
      `,
      after: dedent`
        [[t|]]
        [[]]
        [[#]]
        [[ t ]]
        [[t|a|b]]
        ![[f.png|alt|300]]
        [[https://example.com]]
        [[a]b]]
        \\[[t]]
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    // markdown to wiki
    {
      testName: 'Markdown links are converted to wiki links',
      before: dedent`
        [t](t)
        [d](t)
        Some text [First](First) and [second link](Second) on one line.
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        Some text [[First]] and [[Second|second link]] on one line.
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown link display text is omitted when it matches the default heading display text',
      before: dedent`
        [p > h](p#h)
        [h](#h)
        [p#h](p#h)
        [d](p#h)
      `,
      after: dedent`
        [[p#h]]
        [[#h]]
        [[p#h]]
        [[p#h|d]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'External markdown links are never converted',
      before: dedent`
        [d](https://example.com)
        [d](obsidian://open?vault=vault&file=note)
        [d](file:///C:/notes/note.md)
        [d](mailto:someone@example.com)
        ![alt](https://example.com/f.png)
      `,
      after: dedent`
        [d](https://example.com)
        [d](obsidian://open?vault=vault&file=note)
        [d](file:///C:/notes/note.md)
        [d](mailto:someone@example.com)
        ![alt](https://example.com/f.png)
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown links and images with a title are not converted',
      before: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        [d](<My Page> "title")
        ![alt](f.png "title")
      `,
      after: dedent`
        [d](t "title")
        [d](t 'title')
        [d](t (title))
        [d](<My Page> "title")
        ![alt](f.png "title")
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown links and images that span multiple lines are not converted',
      before: dedent`
        [multi
        line](t)
        ${''}
        [d](
        t)
        ${''}
        [d](t
        )
        ${''}
        [d](t "multi
        line title")
        ${''}
        ![multi
        line](f.png)
      `,
      after: dedent`
        [multi
        line](t)
        ${''}
        [d](
        t)
        ${''}
        [d](t
        )
        ${''}
        [d](t "multi
        line title")
        ${''}
        ![multi
        line](f.png)
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown link destinations using angle brackets are converted',
      before: dedent`
        [d](<My Page>)
        [d]( <My Page> )
        [My Page](<My Page>)
        [d](<My Page#My Heading>)
        [My Page > My Heading](<My Page#My Heading>)
      `,
      after: dedent`
        [[My Page|d]]
        [[My Page|d]]
        [[My Page]]
        [[My Page#My Heading|d]]
        [[My Page#My Heading]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown link destinations with balanced parentheses are converted',
      before: dedent`
        [d](a(b)c)
        [d](Page_(draft))
        [d](a((b)))
      `,
      after: dedent`
        [[a(b)c|d]]
        [[Page_(draft)|d]]
        [[a((b))|d]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown link destinations with unbalanced parentheses are not links and are left alone',
      before: dedent`
        [d](a(b)
      `,
      after: dedent`
        [d](a(b)
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Backslash escapes in markdown link destinations become literal characters in the wiki target',
      before: dedent`
        [d](a\\(b)
        [d](a\\)b)
        [d](<a\\<b\\>>)
        [d](My\\ Page)
        [My Page](My\\ Page)
        [d](a\\#b)
      `,
      after: dedent`
        [[a(b|d]]
        [[a)b|d]]
        [[a<b>|d]]
        [[My Page|d]]
        [[My Page]]
        [[a#b|d]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Percent-encoded markdown link destinations are decoded in the wiki target',
      before: dedent`
        [d](My%20Page)
        [My Page](My%20Page)
        [Three laws of motion](Three%20laws%20of%20motion.md)
        [d](Note.md#Heading%20One)
        ![alt](My%20Image.png)
        [d](100%)
        [d](a%7Cb)
      `,
      after: dedent`
        [[My Page|d]]
        [[My Page]]
        [[Three laws of motion.md|Three laws of motion]]
        [[Note.md#Heading One|d]]
        ![[My Image.png|alt]]
        [[100%|d]]
        [d](a%7Cb)
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown links and images with a nested link or image in their label are left alone',
      before: dedent`
        [![alt](img.png)](Note)
        [a [b](c) d](e)
        ![a [b](c) d](e.png)
        [![[img.png]]](Note)
      `,
      after: dedent`
        [![alt](img.png)](Note)
        [a [b](c) d](e)
        ![a [b](c) d](e.png)
        [![[img.png]]](Note)
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Nested brackets and backslash escapes in markdown link labels are supported',
      before: dedent`
        [a [b] c](t)
        [[a] b](t)
        [a \\[b c](t)
        [a\\]b](t)
        [a\\*b\\*](t)
        [a\\_b](a_b)
        [a\\b](t)
      `,
      after: dedent`
        [[t|a [b] c]]
        [[t|[a] b]]
        [[t|a [b c]]
        [[t|a]b]]
        [[t|a*b*]]
        [[a_b]]
        [[t|a\\b]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown links that do not have a clear wiki link equivalent are left alone',
      before: dedent`
        [](t)
        [d]()
        [d](<>)
        [d](a|b)
        [a|b](t)
        [a [[b]] c](t)
        [d](t#)
        [^1](t)
        [d][ref]
        [d]: t
        \\[d](t)
        [d] (t)
        [d](My Page)
        [d](a\\\\b)
      `,
      after: dedent`
        [](t)
        [d]()
        [d](<>)
        [d](a|b)
        [a|b](t)
        [a [[b]] c](t)
        [d](t#)
        [^1](t)
        [d][ref]
        [d]: t
        \\[d](t)
        [d] (t)
        [d](My Page)
        [d](a\\\\b)
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown images are left alone when only the link style is set',
      before: dedent`
        [d](t) ![alt](f.png)
      `,
      after: dedent`
        [[t|d]] ![alt](f.png)
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown images are converted to wiki embeds',
      before: dedent`
        ![alt](f.png)
        ![](f.png)
        ![f.png](f.png)
        ![alt](<My Image.png>)
        ![alt](folder/f.png)
        [d](t)
      `,
      after: dedent`
        ![[f.png|alt]]
        ![[f.png]]
        ![[f.png]]
        ![[My Image.png|alt]]
        ![[folder/f.png|alt]]
        [d](t)
      `,
      options: {
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Markdown images with display text that Obsidian would treat as a size are left alone',
      before: dedent`
        ![300](f.png)
        ![alt|300](f.png)
      `,
      after: dedent`
        ![300](f.png)
        ![alt|300](f.png)
      `,
      options: {
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Link and image styles can be applied at the same time',
      before: dedent`
        [d](t) ![[f.png]] [[Note]] ![alt](g.png)
      `,
      after: dedent`
        [[t|d]] ![f.png](f.png) [[Note]] ![alt](g.png)
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Converting to markdown and back to wiki results in the original wiki links',
      before: dedent`
        [t](t) [d](t) [p > h](p#h) [h](#h) [My Page](<My Page>) ![f.png](f.png) ![alt](f.png)
      `,
      after: dedent`
        [[t]] [[t|d]] [[p#h]] [[#h]] [[My Page]] ![[f.png]] ![[f.png|alt]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Converting to wiki and back to markdown results in the original markdown links',
      before: dedent`
        [[t]] [[t|d]] [[p#h]] [[#h]] [[My Page]] ![[f.png]] ![[f.png|alt]]
      `,
      after: dedent`
        [t](t) [d](t) [p > h](p#h) [h](#h) [My Page](<My Page>) ![f.png](f.png) ![alt](f.png)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    // do-not-modify regions
    {
      testName: 'Links in YAML frontmatter are not converted',
      before: dedent`
        ---
        related: "[[t]]"
        other: "[d](t)"
        ---
        [[t]]
      `,
      after: dedent`
        ---
        related: "[[t]]"
        other: "[d](t)"
        ---
        [t](t)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Links in code blocks and inline code are not converted',
      before: dedent`
        \`\`\`md
        [[t]] ![[f.png]]
        \`\`\`
        ${''}
        ~~~
        [[t]]
        ~~~
        ${''}
            [[t]]
        ${''}
        \`[[t]]\` and \`\`[[t|d]]\`\` and [[t]]
      `,
      after: dedent`
        \`\`\`md
        [[t]] ![[f.png]]
        \`\`\`
        ${''}
        ~~~
        [[t]]
        ~~~
        ${''}
            [[t]]
        ${''}
        \`[[t]]\` and \`\`[[t|d]]\`\` and [t](t)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Markdown links in code blocks and inline code are not converted',
      before: dedent`
        \`\`\`md
        [d](t) ![alt](f.png)
        \`\`\`
        ${''}
        \`[d](t)\` and [d](t)
      `,
      after: dedent`
        \`\`\`md
        [d](t) ![alt](f.png)
        \`\`\`
        ${''}
        \`[d](t)\` and [[t|d]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Links containing inline code are not converted',
      before: dedent`
        [[t|\`code\`]]
        [\`code\`](t)
      `,
      after: dedent`
        [[t|\`code\`]]
        [\`code\`](t)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Links in math blocks and inline math are not converted',
      before: dedent`
        $$
        [[t]]
        $$
        ${''}
        $[d](t)$ and [[t]]
      `,
      after: dedent`
        $$
        [[t]]
        $$
        ${''}
        $[d](t)$ and [t](t)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Links in HTML blocks are not converted',
      before: dedent`
        <div>
        [[t]] [d](t)
        </div>
        ${''}
        [d](t)
      `,
      after: dedent`
        <div>
        [[t]] [d](t)
        </div>
        ${''}
        [[t|d]]
      `,
      options: {
        linkStyle: 'wiki',
      },
    },
    {
      testName: 'Links in Templater commands are not converted',
      before: dedent`
        <% tp.file.include("[[Template]]") %>
        <%*
        tR += "[d](t)";
        %>
        [[t]]
      `,
      after: dedent`
        <% tp.file.include("[[Template]]") %>
        <%*
        tR += "[d](t)";
        %>
        [t](t)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Links in Obsidian comments are not converted',
      before: dedent`
        %% [[t]] %% [[t]]
        ${''}
        %%
        [[t]]
        [d](t)
        %%
        ${''}
        %%comment [[t]]
        spanning lines%% [[t]]
      `,
      after: dedent`
        %% [[t]] %% [t](t)
        ${''}
        %%
        [[t]]
        [d](t)
        %%
        ${''}
        %%comment [[t]]
        spanning lines%% [t](t)
      `,
      options: {
        linkStyle: 'markdown',
      },
    },
    {
      testName: 'Links in tables are not converted',
      before: dedent`
        | Link | Image |
        | ---- | ----- |
        | [[t\\|d]] | ![[f.png]] |
        | [d](t) | ![alt](f.png) |
        ${''}
        [[t]]
      `,
      after: dedent`
        | Link | Image |
        | ---- | ----- |
        | [[t\\|d]] | ![[f.png]] |
        | [d](t) | ![alt](f.png) |
        ${''}
        [t](t)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Links in custom ignore blocks are not converted',
      before: dedent`
        <!-- linter-disable -->
        [[t]] ![alt](f.png)
        <!-- linter-enable -->
        ${''}
        %% linter-disable %%
        [[t]] ![alt](f.png)
        %% linter-enable %%
        ${''}
        [[t]] ![alt](f.png)
      `,
      after: dedent`
        <!-- linter-disable -->
        [[t]] ![alt](f.png)
        <!-- linter-enable -->
        ${''}
        %% linter-disable %%
        [[t]] ![alt](f.png)
        %% linter-enable %%
        ${''}
        [t](t) ![[f.png|alt]]
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Links in lists, blockquotes, callouts, and headings are converted',
      before: dedent`
        # Heading with [[t]]
        ${''}
        - [[t]]
        - [ ] [[t|d]]
        ${''}
        > [!note] [[t]]
        > ![[f.png|300]]
      `,
      after: dedent`
        # Heading with [t](t)
        ${''}
        - [t](t)
        - [ ] [d](t)
        ${''}
        > [!note] [t](t)
        > ![f.png](f.png)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
  ],
});
