import { EditorSelection, type ChangeSpec } from '@codemirror/state';
import type { Command, EditorView, KeyBinding } from '@codemirror/view';

// Formatting commands edit only the characters they add or remove. They never reformat the
// surrounding text, so a toolbar click cannot reflow a table or change list markers elsewhere.

/** Wraps each selection in marker, or removes marker when the selection is already wrapped. */
export function toggleWrap(marker: string): Command {
  return (view) => {
    if (view.state.readOnly) return false;
    const { state } = view;
    const m = marker.length;
    view.dispatch(
      state.changeByRange((range) => {
        const before = state.sliceDoc(range.from - m, range.from);
        const after = state.sliceDoc(range.to, range.to + m);
        if (before === marker && after === marker) {
          return {
            changes: [
              { from: range.from - m, to: range.from },
              { from: range.to, to: range.to + m },
            ],
            range: EditorSelection.range(range.from - m, range.to - m),
          };
        }
        return {
          changes: [
            { from: range.from, insert: marker },
            { from: range.to, insert: marker },
          ],
          range: EditorSelection.range(range.from + m, range.to + m),
        };
      }),
      { scrollIntoView: true, userEvent: 'input.format' },
    );
    view.focus();
    return true;
  };
}

const LINE_PREFIX = /^(#{1,6} |> |[-*+] \[[ xX]\] |[-*+] |\d+[.)] )/;

/** Sets (or, when already set, removes) a line prefix on every selected line. */
export function toggleLinePrefix(prefix: string): Command {
  return (view) => {
    if (view.state.readOnly) return false;
    const { state } = view;
    const changes: ChangeSpec[] = [];
    const seen = new Set<number>();
    for (const range of state.selection.ranges) {
      for (let pos = range.from; pos <= range.to; ) {
        const line = state.doc.lineAt(pos);
        if (!seen.has(line.number)) {
          seen.add(line.number);
          const existing = LINE_PREFIX.exec(line.text)?.[0] ?? '';
          const insert = existing === prefix ? '' : prefix;
          changes.push({ from: line.from, to: line.from + existing.length, insert });
        }
        pos = line.to + 1;
      }
    }
    view.dispatch({ changes, scrollIntoView: true, userEvent: 'input.format' });
    view.focus();
    return true;
  };
}

/** Inserts a block on its own lines after the cursor's line, using the file's line ending. */
export function insertBlock(lines: string[], cursorLine = 0, cursorColumn = 0): Command {
  return (view) => {
    if (view.state.readOnly) return false;
    const { state } = view;
    const nl = state.lineBreak;
    const line = state.doc.lineAt(state.selection.main.head);
    const lead = line.text.trim() === '' ? '' : nl + nl;
    const text = lead + lines.join(nl) + nl;
    const from = line.text.trim() === '' ? line.from : line.to;
    const before = lead.length + lines.slice(0, cursorLine).reduce((n, l) => n + l.length + nl.length, 0);
    view.dispatch({
      changes: { from, to: line.text.trim() === '' ? line.to : from, insert: text },
      selection: { anchor: from + before + cursorColumn },
      scrollIntoView: true,
      userEvent: 'input.format',
    });
    view.focus();
    return true;
  };
}

export const insertLink: Command = (view) => {
  if (view.state.readOnly) return false;
  const { state } = view;
  const range = state.selection.main;
  const label = state.sliceDoc(range.from, range.to) || 'link text';
  const text = `[${label}](https://)`;
  const urlStart = range.from + label.length + 3;
  view.dispatch({
    changes: { from: range.from, to: range.to, insert: text },
    selection: { anchor: urlStart, head: urlStart + 'https://'.length },
    scrollIntoView: true,
    userEvent: 'input.format',
  });
  view.focus();
  return true;
};

export const TABLE_TEMPLATE = ['| Column | Column |', '| ------ | ------ |', '| Cell   | Cell   |'];

export const commands = {
  bold: toggleWrap('**'),
  italic: toggleWrap('*'),
  strike: toggleWrap('~~'),
  code: toggleWrap('`'),
  link: insertLink,
  h1: toggleLinePrefix('# '),
  h2: toggleLinePrefix('## '),
  h3: toggleLinePrefix('### '),
  bullet: toggleLinePrefix('- '),
  numbered: toggleLinePrefix('1. '),
  task: toggleLinePrefix('- [ ] '),
  quote: toggleLinePrefix('> '),
  table: insertBlock(TABLE_TEMPLATE, 0, 2),
  codeBlock: insertBlock(['```', '', '```'], 1, 0),
  rule: insertBlock(['---']),
} satisfies Record<string, Command>;

export const formattingKeymap: KeyBinding[] = [
  { key: 'Mod-b', run: commands.bold },
  { key: 'Mod-i', run: commands.italic },
  { key: 'Mod-Shift-x', run: commands.strike },
  { key: 'Mod-e', run: commands.code },
  { key: 'Mod-k', run: commands.link },
  { key: 'Mod-1', run: commands.h1 },
  { key: 'Mod-2', run: commands.h2 },
  { key: 'Mod-3', run: commands.h3 },
];

export type FormatCommand = keyof typeof commands;
export const runFormat = (view: EditorView, name: FormatCommand) => commands[name](view);
