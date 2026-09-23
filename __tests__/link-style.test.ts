import LinkStyle from '../src/rules/link-style';
import dedent from 'ts-dedent';
import {ruleTest} from './common';

ruleTest({
  RuleBuilderClass: LinkStyle,
  testCases: [
    {
      testName: 'Wiki to markdown handles headings and embed sizes',
      before: dedent`
        [[#h]] [[p|d]] ![[f.png|300x200]] ![[f.png|alt]]
      `,
      after: dedent`
        [h](#h) [d](p) ![f.png](f.png) ![alt](f.png)
      `,
      options: {linkStyle: 'markdown', imageStyle: 'markdown'},
    },
    {
      testName: 'Markdown to wiki handles escapes, angle brackets, parens, titles and ignored regions',
      before: dedent`
        [a [b] c](<My Page>) [a\\*b](t) [d]( <My Page> ) [x](a(b)c) [y](a\\ b\\)) [t](t "title")
        ![](f.png) ![f.png](f.png) [p > h](p#h) \`[d](t)\` [multi
        line](t)
        | [d](t) |
        | --- |
      `,
      after: dedent`
        [[My Page|a [b] c]] [[t|a*b]] [[My Page|d]] [[a(b)c|x]] [[a b)|y]] [t](t "title")
        ![[f.png]] ![[f.png]] [[p#h]] \`[d](t)\` [multi
        line](t)
        | [d](t) |
        | --- |
      `,
      options: {linkStyle: 'wiki', imageStyle: 'wiki'},
    },
  ],
});
