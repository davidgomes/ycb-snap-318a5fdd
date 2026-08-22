import TrailingSpaces from '../src/rules/trailing-spaces';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: TrailingSpaces,
  testCases: [
    {
      testName: 'One trailing space removed',
      before: dedent`
        # H1 ${''}
        line with one trailing spaces ${''}
      `,
      after: dedent`
        # H1
        line with one trailing spaces
      `,
    },
    {
      testName: 'Three trailing whitespaces removed',
      before: dedent`
        # H1   ${''}
        line with three trailing spaces   ${''}
      `,
      after: dedent`
        # H1
        line with three trailing spaces
      `,
    },
    {
      testName: 'Tab-Space-Linebreak removed',
      before: dedent`
        # H1
        line with trailing tab and spaces    ${''}
        ${''}
      `,
      after: dedent`
        # H1
        line with trailing tab and spaces
        ${''}
      `,
      options: {
        twoSpaceLineBreak: true,
      },
    },
    {
      testName: 'Two Space Linebreak not removed',
      before: dedent`
        # H1
        line with one trailing spaces  ${''}
        ${''}
      `,
      after: dedent`
        # H1
        line with one trailing spaces  ${''}
        ${''}
      `,
      options: {
        twoSpaceLineBreak: true,
      },
    },
    {
      testName: 'Regular link with spaces stays the same',
      before: dedent`
        # Hello world
        ${''}
        [This has  spaces in it](File with  spaces.md)
      `,
      after: dedent`
        # Hello world
        ${''}
        [This has  spaces in it](File with  spaces.md)
      `,
    },
    {
      testName: 'Image link with spaces stays the same',
      before: dedent`
        # Hello world
        ${''}
        ![This has  spaces in it](File with  spaces.png)
      `,
      after: dedent`
        # Hello world
        ${''}
        ![This has  spaces in it](File with  spaces.png)
      `,
    },
    {
      testName: 'Wiki link with spaces stays the same',
      before: dedent`
        # Hello world
        ${''}
        [[File with  spaces]]
      `,
      after: dedent`
        # Hello world
        ${''}
        [[File with  spaces]]
      `,
    },
    { // accounts for https://github.com/platers/obsidian-linter/issues/868
      testName: 'A list item with no content should not have the space indicating it is a list item removed',
      before: dedent`
        - List item 1
        -${' '}
        -${'   '}
      `,
      after: dedent`
        - List item 1
        -${' '}
        -${' '}
      `,
    },
    { // relates to for https://github.com/platers/obsidian-linter/issues/868
      testName: 'Make sure that checklists are properly handled with trailing spaces',
      before: dedent`
        - [ ] List item 1
        - [ ]${' '}
        - [ ] ${'   '}
      `,
      after: dedent`
        - [ ] List item 1
        - [ ]${' '}
        - [ ]${' '}
      `,
    },
    { // relates to for https://github.com/platers/obsidian-linter/issues/868
      testName: 'Make sure that indented lists are properly handled with trailing spaces',
      before: dedent`
        Text here
        - List item 1
          -${' '}
          -${'   '}
      `,
      after: dedent`
        Text here
        - List item 1
          -${' '}
          -${' '}
      `,
    },
    { // relates to for https://github.com/platers/obsidian-linter/issues/868
      testName: 'Make sure that ordered lists are properly handled with trailing spaces',
      before: dedent`
        Text here
        1. List item 1
        2.${' '}
        3.${'   '}
      `,
      after: dedent`
        Text here
        1. List item 1
        2.${' '}
        3.${' '}
      `,
    },
    {
      testName: 'Scoped disable markers skip trailing spaces for listed rules only',
      before: dedent`
        kept spaces  ${''}
        <!-- linter-disable trailing-spaces -->
        ignored spaces  ${''}
        <!-- linter-enable trailing-spaces -->
        after spaces  ${''}
      `,
      after: dedent`
        kept spaces
        <!-- linter-disable trailing-spaces -->
        ignored spaces  ${''}
        <!-- linter-enable trailing-spaces -->
        after spaces
      `,
    },
    {
      testName: 'Obsidian next-line disable keeps trailing spaces on the following line',
      before: dedent`
        %% linter-disable-next-line trailing-spaces %%
        next line spaces  ${''}
        later spaces  ${''}
      `,
      after: dedent`
        %% linter-disable-next-line trailing-spaces %%
        next line spaces  ${''}
        later spaces
      `,
    },
    {
      testName: 'Next-n-lines disable is clamped to the end of the file',
      before: dedent`
        <!-- linter-disable-next-n-lines: 5 trailing-spaces -->
        one  ${''}
        two  ${''}
      `,
      after: dedent`
        <!-- linter-disable-next-n-lines: 5 trailing-spaces -->
        one  ${''}
        two  ${''}
      `,
    },
    {
      testName: 'Disable all then re-enable trailing-spaces formats only the re-enabled lines',
      before: dedent`
        <!-- linter-disable -->
        still ignored  ${''}
        <!-- linter-enable trailing-spaces -->
        formatted again  ${''}
        <!-- linter-enable -->
        after  ${''}
      `,
      after: dedent`
        <!-- linter-disable -->
        still ignored  ${''}
        <!-- linter-enable trailing-spaces -->
        formatted again
        <!-- linter-enable -->
        after
      `,
    },
    { // accounts for https://github.com/platers/obsidian-linter/issues/1329
      testName: 'Make sure that we properly handle an empty list item when it is empty and has no spaces in it',
      before: dedent`
        Some text
        ${''}
        -
        ${''}
      `,
      after: dedent`
        Some text
        ${''}
        -
        ${''}
      `,
    },
  ],
});
