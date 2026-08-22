import AutoToc from '../src/rules/auto-toc';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: AutoToc,
  testCases: [
    {
      testName: 'Leaves notes without a TOC marker unchanged',
      before: dedent`
        ## Heading
      `,
      after: dedent`
        ## Heading
      `,
    },
    {
      testName: 'Generates a nested bullet list',
      before: dedent`
        <!-- toc -->
        old contents
        <!-- /toc -->

        # Title
        ## Introduction
        ### Details
      `,
      after: dedent`
        <!-- toc -->

        - [Introduction](#introduction)
          - [Details](#details)

        <!-- /toc -->

        # Title
        ## Introduction
        ### Details
      `,
    },
    {
      testName: 'Supports numbered lists, explicit IDs, and exclusions',
      before: dedent`
        <!-- TOC -->
        <!-- /TOC -->

        ## Included {#custom-id}
        ### Skip me
        #### Included too
      `,
      after: dedent`
        <!-- TOC -->

        1. [Included](#custom-id)
            1. [Included too](#included-too)

        <!-- /TOC -->
      `,
      options: {
        listStyle: 'number',
        useExplicitIds: true,
        excludeHeadings: ['skip me'],
      },
    },
    {
      testName: 'Ignores headings in YAML, code, math, and the TOC',
      before: dedent`
        ---
        title: "# YAML heading"
        ---

        <!-- toc -->
        ## Existing heading
        <!-- /toc -->

        \`\`\`
        ## Code heading
        \`\`\`

        $$
        ## Math heading
        $$

        ## Real heading
      `,
      after: dedent`
        ---
        title: "# YAML heading"
        ---

        <!-- toc -->

        - [Real heading](#real-heading)

        <!-- /toc -->

        \`\`\`
        ## Code heading
        \`\`\`

        $$
        ## Math heading
        $$

        ## Real heading
      `,
    },
  ],
});
