'use strict';
/* UI translations. Values are plain strings or functions of arguments.
   Static HTML uses data-i18n (text) and data-i18n-title (tooltip) attributes. */

const I18N = {
  en: {
    locale: 'en-GB',
    // toolbar
    against: 'Against',
    left: 'Left',
    right: 'Right',
    leftTitle: 'Left side: the common ancestor of the branches (like a pull request), or the current tip of the base branch',
    rightTitle: 'Right side: the working tree including uncommitted and new files, or commits only',
    optMergebase: 'merge-base (like PR)',
    optTip: 'tip of base branch',
    optWorktree: 'working tree',
    optHead: 'HEAD (commits only)',
    ignoreWs: 'Ignore whitespace',
    comments: 'Comments',
    commentsTitle: 'List of comments',
    exportTitle: 'Write the comments to REVIEW.md in the repository root',
    copy: 'Copy',
    copyTitle: 'Copy the comments as a prompt for Claude',
    settings: 'Settings',
    theme: 'Theme',
    themeDark: 'Dark',
    themeLight: 'Light',
    language: 'Language',
    loading: 'Loading…',
    notExists: 'does not exist',
    otherBranches: 'Other branches',
    worktree: 'working tree',
    // statuses
    st: { M: 'modified', A: 'added', D: 'deleted', R: 'renamed', U: 'new (untracked)', T: 'type changed' },
    // sidebar
    files: n => n === 1 ? 'file' : 'files',
    onBase: b => `You are on the ${b} branch itself.`,
    noChangesOnBase: 'No changes – you are on the base branch.',
    noChanges: 'No changes.',
    noChangesVsBase: 'No changes against the selected branch.',
    reviewed: 'Reviewed',
    changedSince: 'Changed since you last viewed it',
    // diff
    folded: n => `⋯ ${n} unchanged lines – click to show`,
    fileMissing: 'file does not exist',
    hint: 'F7 / Shift+F7 changes · Alt+↓/↑ files · click a line number = comment',
    binary: 'Binary file – no preview available.',
    tooLarge: size => `The file is large (${size}).`,
    showAnyway: 'Show anyway',
    renamedOnly: old => `The file was only renamed from <b>${old}</b>, the content is unchanged.`,
    emptyFile: 'The file is empty.',
    wsOnly: 'The file differs only in whitespace (“Ignore whitespace” is on).',
    sameContent: 'The file content is the same (mode or type change).',
    notInChanges: 'The file is no longer among the changes.',
    // comments
    sideNew: 'new version',
    sideOld: 'original version',
    line: 'line',
    stale: 'outdated',
    staleTitle: t => `The line has changed since the comment was written. Originally: ${t}`,
    edit: 'Edit',
    delete: 'Delete',
    addComment: 'Add comment',
    saveChanges: 'Save changes',
    cancel: 'Cancel',
    placeholder: 'Comment for Claude…',
    editorHint: 'Ctrl+Enter saves · Esc cancels',
    orphans: 'Comments on lines that are not shown now',
    clearAll: 'Delete all',
    clearConfirm: 'Really delete all?',
    noComments: 'No comments yet. Click a line number in the diff.',
    emptyComment: 'The comment is empty',
    fileNotCompared: 'The file is not in the current comparison',
    // export
    nothingToExport: 'No comments to export',
    written: (n, p) => `Wrote ${n} ${n === 1 ? 'comment' : 'comments'} to ${p} – tell Claude “address REVIEW.md”`,
    copied: n => `Copied ${n} ${n === 1 ? 'comment' : 'comments'} to the clipboard`,
    copyFailed: 'Copying to the clipboard failed',
    // fetch
    fetch: 'Fetch',
    fetching: 'Fetching…',
    fetchTitle: 'git fetch --all --prune, then fast-forward local develop/master/main (and the selected base) to the remote',
    behindTitle: (b, up, behind, ahead) => `Local ${b} is ${behind} commit(s) behind ${up}` +
      (ahead ? ` and has ${ahead} local commit(s)` : '') + ' (as of the last fetch). Click Fetch to update it.',
    fetched: 'Fetched.',
    ffUpdated: (b, n, up) => `${b}: +${n} ${n === 1 ? 'commit' : 'commits'} from ${up}`,
    ffUptodate: b => `${b}: up to date`,
    ffDiverged: (b, a, up) => `${b}: has ${a} local ${a === 1 ? 'commit' : 'commits'}, not updated – compare against ${up}`,
    ffCheckedout: b => `${b}: checked out, not updated – use git pull`,
    nothingToUpdate: 'No local base branches to update.',
    // navigation, live
    lastChange: 'Last change of the last file',
    firstChange: 'First change of the first file',
    liveOn: 'Auto-refresh: connected',
    liveOff: 'Auto-refresh: disconnected, retrying…',
  },

  cs: {
    locale: 'cs-CZ',
    against: 'Proti',
    left: 'Vlevo',
    right: 'Vpravo',
    leftTitle: 'Levá strana: společný předek větví (jako Pull Request), nebo aktuální špička base větve',
    rightTitle: 'Pravá strana: pracovní kopie včetně necommitnutých a nových souborů, nebo jen commity',
    optMergebase: 'merge-base (jako PR)',
    optTip: 'špička base větve',
    optWorktree: 'pracovní kopie',
    optHead: 'HEAD (jen commity)',
    ignoreWs: 'Ignorovat mezery',
    comments: 'Komentáře',
    commentsTitle: 'Seznam komentářů',
    exportTitle: 'Zapsat komentáře do REVIEW.md v kořeni repozitáře',
    copy: 'Kopírovat',
    copyTitle: 'Zkopírovat komentáře jako prompt pro Claude',
    settings: 'Nastavení',
    theme: 'Téma',
    themeDark: 'Tmavé',
    themeLight: 'Světlé',
    language: 'Jazyk',
    loading: 'Načítám…',
    notExists: 'neexistuje',
    otherBranches: 'Ostatní větve',
    worktree: 'pracovní kopie',
    st: { M: 'změněn', A: 'přidán', D: 'smazán', R: 'přejmenován', U: 'nový (nesledovaný)', T: 'změna typu' },
    files: n => n === 1 ? 'soubor' : n >= 2 && n <= 4 ? 'soubory' : 'souborů',
    onBase: b => `Jsi přímo na větvi ${b}.`,
    noChangesOnBase: 'Žádné změny – jsi na base větvi.',
    noChanges: 'Žádné změny.',
    noChangesVsBase: 'Žádné změny oproti zvolené větvi.',
    reviewed: 'Zkontrolováno',
    changedSince: 'Změněno od posledního zobrazení',
    folded: n => `⋯ ${n} nezměněných řádků – klikni pro zobrazení`,
    fileMissing: 'soubor neexistuje',
    hint: 'F7 / Shift+F7 změny · Alt+↓/↑ soubory · klik na číslo řádku = komentář',
    binary: 'Binární soubor – náhled není k dispozici.',
    tooLarge: size => `Soubor je velký (${size}).`,
    showAnyway: 'Zobrazit i tak',
    renamedOnly: old => `Soubor byl jen přejmenován z <b>${old}</b>, obsah je beze změny.`,
    emptyFile: 'Soubor je prázdný.',
    wsOnly: 'Soubor se liší jen v bílých znacích (zapnuto „Ignorovat mezery“).',
    sameContent: 'Obsah souboru se neliší (změna oprávnění nebo typu).',
    notInChanges: 'Soubor už není mezi změnami.',
    sideNew: 'nová verze',
    sideOld: 'původní verze',
    line: 'řádek',
    stale: 'zastaralý',
    staleTitle: t => `Řádek se od napsání komentáře změnil. Původně: ${t}`,
    edit: 'Upravit',
    delete: 'Smazat',
    addComment: 'Přidat komentář',
    saveChanges: 'Uložit změny',
    cancel: 'Zrušit',
    placeholder: 'Komentář pro Claude…',
    editorHint: 'Ctrl+Enter uloží · Esc zruší',
    orphans: 'Komentáře k řádkům, které se teď nezobrazují',
    clearAll: 'Smazat vše',
    clearConfirm: 'Opravdu smazat vše?',
    noComments: 'Zatím žádné komentáře. Klikni na číslo řádku v diffu.',
    emptyComment: 'Komentář je prázdný',
    fileNotCompared: 'Soubor není v aktuálním porovnání',
    nothingToExport: 'Žádné komentáře k exportu',
    written: (n, p) => `Zapsáno ${n} připomínek do ${p} – v Claude napiš „zapracuj REVIEW.md“`,
    copied: n => `Zkopírováno ${n} připomínek do schránky`,
    copyFailed: 'Kopírování do schránky selhalo',
    fetch: 'Fetch',
    fetching: 'Stahuji…',
    fetchTitle: 'git fetch --all --prune a pak fast-forward lokálních develop/master/main (a zvolené base) na stav z remote',
    behindTitle: (b, up, behind, ahead) => `Lokální ${b} je o ${behind} commitů pozadu za ${up}` +
      (ahead ? ` a má ${ahead} lokálních commitů` : '') + ' (podle posledního fetche). Aktualizuješ tlačítkem Fetch.',
    fetched: 'Staženo.',
    ffUpdated: (b, n, up) => `${b}: +${n} commitů z ${up}`,
    ffUptodate: b => `${b}: aktuální`,
    ffDiverged: (b, a, up) => `${b}: má ${a} lokálních commitů, neaktualizováno – porovnej proti ${up}`,
    ffCheckedout: b => `${b}: je checkoutnutá, neaktualizováno – použij git pull`,
    nothingToUpdate: 'Žádné lokální base větve k aktualizaci.',
    lastChange: 'Poslední změna posledního souboru',
    firstChange: 'První změna prvního souboru',
    liveOn: 'Auto-refresh: připojeno',
    liveOff: 'Auto-refresh: odpojeno, zkouším znovu…',
  },
};

let uiLang = 'en';

/* t('key', ...args) returns the translation in the current language,
   falling back to English. */
function t(key, ...args) {
  const v = I18N[uiLang][key] ?? I18N.en[key] ?? key;
  return typeof v === 'function' ? v(...args) : v;
}

/* Fills elements marked with data-i18n / data-i18n-title. */
function applyStaticTexts(root = document) {
  document.documentElement.lang = uiLang;
  root.querySelectorAll('[data-i18n]').forEach(el => { el.textContent = t(el.dataset.i18n); });
  root.querySelectorAll('[data-i18n-title]').forEach(el => { el.title = t(el.dataset.i18nTitle); });
}

/* Initial language: saved choice, else the browser's language. */
function defaultLang() {
  const nav = (navigator.languages && navigator.languages[0]) || navigator.language || 'en';
  return nav.toLowerCase().startsWith('cs') ? 'cs' : 'en';
}
