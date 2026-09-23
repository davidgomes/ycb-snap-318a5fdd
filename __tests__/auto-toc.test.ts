import dedent from 'ts-dedent';
import AutoToc from '../src/rules/auto-toc';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Leaves text unchanged when the toc marker is absent',
      before: dedent`
        # Title
        ## Section
      `,
      after: dedent`
        # Title
        ## Section
      `,
    },
    {
      testName: 'Skips headings in code, math, and yaml and dedupes anchors',
      before: dedent`
        ---
        ## not a heading
        ---
        <!-- toc -->
        \`\`\`
        ## Code
        \`\`\`
        $$
        ## Math
        $$
        ## Hello World!
        ## Hello World!
        ## See [the docs](https://example.com)
        ## Pic ![[shot.png]] here
      `,
      after: dedent`
        ---
        ## not a heading
        ---
        <!-- toc -->

        - [Hello World!](#hello-world)
        - [Hello World!](#hello-world-1)
        - [See [the docs](https://example.com)](#see-the-docs)
        - [Pic ![[shot.png]] here](#pic-here)

        <!-- /toc -->

        \`\`\`
        ## Code
        \`\`\`
        $$
        ## Math
        $$
        ## Hello World!
        ## Hello World!
        ## See [the docs](https://example.com)
        ## Pic ![[shot.png]] here
      `,
    },
    {
      testName: 'Regex exclusions and always-one numbering',
      before: dedent`
        <!-- toc -->
        <!-- /toc -->
        ## Appendix A
        ## Body
      `,
      after: dedent`
        <!-- toc -->

        1. [Body](#body)

        <!-- /toc -->

        ## Appendix A
        ## Body
      `,
      options: {
        listStyle: 'number',
        orderedListStyle: 'always-one',
        excludeHeadings: ['/^appendix/'],
      },
    },
  ],
});
