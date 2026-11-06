# gitreview

[![Release](https://img.shields.io/github/v/release/JanVrana/gitreview)](https://github.com/JanVrana/gitreview/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/JanVrana/gitreview)](go.mod)
[![Licence: MIT](https://img.shields.io/badge/licence-MIT-green)](LICENSE)
![Platformy](https://img.shields.io/badge/platformy-linux%20%7C%20macOS-blue)
![Závislosti](https://img.shields.io/badge/závislosti-žádné-brightgreen)

[English](README.md) · **Čeština**

Webový nástroj pro review změn aktuální git větve proti `develop` / `master`. Diff se zobrazuje vedle sebe (vlevo originál, vpravo změny) ve stylu IntelliJ. Je určený pro kontrolu práce Claude Code na VM, kde nejsou po ruce desktopové nástroje.

Výsledkem je jedna Go binárka bez závislostí, frontend je v ní zabudovaný (offline, žádné CDN).

![Diff v tmavém tématu s review komentářem](docs/screenshots/diff-dark.png)

<table>
<tr>
<td><img src="docs/screenshots/word-diff.png" alt="Zvýraznění změněných slov"><br><sub>Zvýraznění změněných slov uvnitř řádku</sub></td>
<td><img src="docs/screenshots/diff-light.png" alt="Světlé téma"><br><sub>Světlé téma, sbalené nezměněné úseky</sub></td>
<td><img src="docs/screenshots/comments-panel.png" alt="Panel komentářů"><br><sub>Panel komentářů a export REVIEW.md</sub></td>
</tr>
</table>

## Instalace

Linux / macOS (amd64, arm64):

```bash
curl -fsSL https://raw.githubusercontent.com/JanVrana/gitreview/main/install.sh | sh
```

[Skript](install.sh) stáhne binárku z posledního releasu, ověří její SHA-256 součet a nainstaluje ji do `~/.local/bin`. Jiný adresář nastavíš přes `GITREVIEW_INSTALL_DIR`, konkrétní verzi přes `GITREVIEW_VERSION=v0.2.0`, např.:

```bash
curl -fsSL https://raw.githubusercontent.com/JanVrana/gitreview/main/install.sh | GITREVIEW_INSTALL_DIR=/usr/local/bin sudo -E sh
```

Bez skriptu můžeš binárku stáhnout přímo:

```bash
mkdir -p ~/.local/bin && curl -fL -o ~/.local/bin/gitreview \
  "https://github.com/JanVrana/gitreview/releases/latest/download/gitreview-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" \
  && chmod +x ~/.local/bin/gitreview && gitreview -version
```

Pokud `~/.local/bin` nemáš v `PATH`, dej binárku jinam (např. `sudo` do `/usr/local/bin`). Kontrolní součty jsou v `SHA256SUMS` u každého releasu.

S nainstalovaným Go (1.26+):

```bash
go install github.com/JanVrana/gitreview@latest
```

Ze zdrojáků: `git clone https://github.com/JanVrana/gitreview && cd gitreview && make build`.

Aktualizace = znovu spustit stejný příkaz.

## Spuštění

```bash
gitreview /cesta/k/repu          # výchozí: aktuální adresář, port 8765, 0.0.0.0
gitreview -p 9000 -b 127.0.0.1 .
```

Potřebuje jen `git` v `PATH`.

Při startu se vypíšou URL (IP adresy VM) i s přístupovým tokenem, např. `http://192.168.1.20:8765/?token=3f9c…`. Otevři některou z nich v prohlížeči. Prohlížeč si token uloží do cookie, takže příště stačí i adresa bez tokenu.

| Přepínač | Význam |
|---|---|
| `-p` | port (výchozí `8765`) |
| `-b` | adresa, na které server poslouchá (výchozí `0.0.0.0`) |
| `-allow-host` | další povolená jména v hlavičce `Host`, oddělená čárkou |
| `-token` | pevný přístupový token místo náhodného (také `GITREVIEW_TOKEN`); záložky pak fungují i po restartu |
| `-no-auth` | bez tokenu: UI může používat každý, kdo se dostane na port |
| `-version` | vypíše verzi a skončí |

**Bezpečnost:** každý požadavek potřebuje **přístupový token**. Ve výchozím stavu se při každém startu vygeneruje náhodný a vypíše se v URL. Pevný nastavíš přes `-token` nebo `GITREVIEW_TOKEN` (raději proměnnou, argumenty jsou vidět v `ps`). Token se přijímá jako `?token=` (vymění se za HttpOnly cookie a z adresy zmizí), jako cookie, nebo pro skripty jako `Authorization: Bearer <token>`.

`-no-auth` token vypne, třeba na privátní VM nebo za SSH tunelem (`-b 127.0.0.1` + `ssh -L 8765:localhost:8765 vm`). Server běží na prostém http, takže v nedůvěryhodné síti jde token odposlechnout; tam použij tunel.

Další ochrany, aktivní i s `-no-auth`:
- Zápisy (komentáře, export) přijímá jen jako JSON ze stejného originu. Cizí web otevřený v prohlížeči tak nemůže podstrčit text do REVIEW.md.
- Přístup přes doménové jméno je kvůli DNS rebindingu povolený jen pro IP adresy, `localhost` a hostname stroje. Další jména povolíš přes `-allow-host vm.firma.cz,dev-vm`.
- Číst lze jen soubory ze seznamu změn.
- Do repozitáře server jen čte, zapisuje pouze `REVIEW.md` a `.git/gitreview/comments.json`.

## Co umí

- **Porovnání:**
  - base `develop` / `master` / `main` / libovolná větev (fallback na `origin/<větev>`),
  - vlevo *merge-base* (jako PR) nebo *špička base*,
  - vpravo *pracovní kopie* (včetně necommitnutých a nových souborů) nebo *HEAD*,
  - přepínač *Ignorovat mezery*.
- **Seznam souborů:** strom souborů s barvami stavů jako v IntelliJ, počty +/− a checkbox „zkontrolováno“. Ten se sám zruší, když se soubor znovu změní.
- **Diff:**
  - zarovnané bloky změn se zvýrazněním změněných slov a zvýrazněním syntaxe (highlight.js),
  - sbalené nezměněné úseky,
  - přehledová lišta změn vpravo,
  - nezávislý horizontální scroll stran.
- **Auto-refresh:** když Claude změní soubor, commitne nebo přepne větev, UI se samo obnoví (polling ~1,5 s přes SSE).
- **Review komentáře:**
  - klikem na číslo řádku přidáš komentář,
  - **Export REVIEW.md** zapíše komentáře do kořene repa a v Claude pak stačí napsat *„zapracuj REVIEW.md“*,
  - **Kopírovat** (v anglickém UI **Copy**) dá totéž do schránky (funguje i na plain http).
  - Komentáře se ukládají do `.git/gitreview/comments.json`. `REVIEW.md` se automaticky přidá do `.git/info/exclude`, takže se necommitne.
  - Když se řádek pod komentářem změní, komentář se označí jako „zastaralý“.
- **Nastavení (⚙):** tmavé (Darcula) nebo světlé téma, angličtina nebo čeština. Výchozí jazyk se řídí prohlížečem; exportovaný `REVIEW.md` je v jazyce UI.

## Klávesy

| Klávesa | Akce |
|---|---|
| F7 / Shift+F7 | další / předchozí změna (na konci souboru přejde na další soubor) |
| Alt+↓ / Alt+↑ | další / předchozí soubor |
| Ctrl+Enter / Esc | uložit / zrušit komentář |
| Shift+kolečko | horizontální scroll strany pod kurzorem |

## Vývoj

```bash
make test   # go test ./...
make vet
make dist   # release binárky do dist/ (gitreview-<os>-<arch> + SHA256SUMS)
```

Struktura:
- `internal/git`: volání git CLI (`--no-optional-locks`, bez shellu, validace refů a cest)
- `internal/diff`: převod `git diff -U0` na zarovnané bloky
- `internal/review`: komentáře a export
- `internal/lang`: mapování souborů na jazyk (pro zvýraznění syntaxe i export)
- `internal/server`: HTTP API, ochrana proti CSRF a DNS rebindingu, sdílený SSE watcher
- `web/`: vanilla JS frontend

## Licence

[MIT](LICENSE). Přibalený [highlight.js](https://highlightjs.org/) (`web/vendor/`) je pod licencí BSD-3-Clause.
