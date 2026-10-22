import type { MarkdownIt, Token } from 'markdown-it';

// GFM task list items: "- [ ] todo" and "- [x] done". The checkbox is display-only; the
// source text is the document, so ticking happens in the source.

const TASK = /^\[([ xX])\][ \t]/;

export function tasksPlugin(md: MarkdownIt): void {
  md.core.ruler.after('inline', 'gfm_tasks', (state) => {
    const tokens = state.tokens;
    const lists: Token[] = [];
    for (let i = 0; i < tokens.length; i++) {
      const t = tokens[i]!;
      if (t.type === 'bullet_list_open' || t.type === 'ordered_list_open') lists.push(t);
      else if (t.type === 'bullet_list_close' || t.type === 'ordered_list_close') lists.pop();
      if (t.type !== 'inline') continue;
      if (tokens[i - 1]?.type !== 'paragraph_open' || tokens[i - 2]?.type !== 'list_item_open')
        continue;
      const first = t.children?.[0];
      if (!first || first.type !== 'text') continue;
      const match = TASK.exec(first.content);
      if (!match) continue;

      first.content = first.content.slice(match[0].length);
      const box = new state.Token('html_inline', '', 0);
      const checked = match[1] !== ' ';
      box.content = `<input class="task-list-item-checkbox" type="checkbox" disabled${checked ? ' checked' : ''}> `;
      t.children!.unshift(box);
      tokens[i - 2]!.attrJoin('class', 'task-list-item');
      lists.at(-1)?.attrJoin('class', 'contains-task-list');
    }
  });
}
