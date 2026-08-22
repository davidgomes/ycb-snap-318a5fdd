import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Converts wiki links and embeds to markdown',
      before: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        ![[f.png]]
        ![[f.png|300]]
        ![[f.png|300x200]]
      `,
      after: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        ![f.png](f.png)
        ![f.png](f.png)
        ![f.png](f.png)
      `,
      options: {
        linkStyle: 'markdown',
        imageStyle: 'markdown',
      },
    },
    {
      testName: 'Converts supported inline markdown links and images to wiki style',
      before: dedent`
        [t](t)
        [d](t)
        [p > h](p#h)
        [h](#h)
        ![alt](f.png)
        ![](empty.png)
        [nested [label]](My\\ Page)
        [balanced](folder/(note).md)
        [angle]( <My Page> )
        [escaped](folder\\(name\\)\\ \\<note\\>)
      `,
      after: dedent`
        [[t]]
        [[t|d]]
        [[p#h]]
        [[#h]]
        ![[f.png|alt]]
        ![[empty.png]]
        [[My Page|nested [label]]]
        [[folder/(note).md|balanced]]
        [[My Page|angle]]
        [[folder(name) <note>|escaped]]
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
    {
      testName: 'Leaves external links, titled links, multiline links, and protected regions unchanged',
      before: dedent`
        [external](https://example.com)
        [titled](note.md "title")
        [multiline
        label](note.md)
        ---
        key: [[yaml]]
        ---
        \`[[inline]]\`
        \`\`\`
        [[code]]
        \`\`\`
        $[[math]]$
        <span>[[html]]</span>
        <% [[templater]] %>
        %% [[comment]] %%
        <!-- linter-disable -->
        [[ignored]]
        <!-- linter-enable -->
        | [[table]] |
        | --- |
      `,
      after: dedent`
        [external](https://example.com)
        [titled](note.md "title")
        [multiline
        label](note.md)
        ---
        key: [[yaml]]
        ---
        \`[[inline]]\`
        \`\`\`
        [[code]]
        \`\`\`
        $[[math]]$
        <span>[[html]]</span>
        <% [[templater]] %>
        %% [[comment]] %%
        <!-- linter-disable -->
        [[ignored]]
        <!-- linter-enable -->
        | [[table]] |
        | --- |
      `,
      options: {
        linkStyle: 'wiki',
        imageStyle: 'wiki',
      },
    },
  ],
});
