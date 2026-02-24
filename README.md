# WinPiBooster

![CI](https://github.com/boomboxOff/WinPiBooster/actions/workflows/ci.yml/badge.svg)

Binaire Windows de surveillance et d'installation automatique des mises à jour Windows.

## Fonctionnement

- Vérifie toutes les **60 secondes** si des mises à jour Windows sont disponibles
- Installe automatiquement les mises à jour détectées avec redémarrage automatique si nécessaire
- Envoie une **notification Windows toast** lors de l'installation de mises à jour ou en cas d'erreur fatale
- Génère un **rapport quotidien à minuit** avec archivage du log et remise à zéro des compteurs
- Génère un **rapport hebdomadaire chaque dimanche à minuit** avec les totaux de la semaine
- Archive les logs à chaque lancement, à minuit et lors d'un dépassement de taille (**10 MB** par défaut)
- Supprime les archives de plus de **30 jours** automatiquement
- Envoie un **heartbeat toutes les heures** avec uptime et compteurs
- **Instance unique** : empêche le lancement de plusieurs instances interactives simultanées
- Écrit un fichier **status.json** après chaque cycle réussi
- Vérifie l'**espace disque libre** avant d'installer (seuil configurable)

## Prérequis

- Windows 10/11 (amd64)
- PowerShell (le module **PSWindowsUpdate** est installé automatiquement si absent)
- Droits administrateur

## Installation

1. Cloner ce dépôt :
   ```bat
   git clone https://github.com/boomboxOff/WinPiBooster.git
   ```
2. Télécharger `WinPiBooster.exe` depuis la [dernière release](https://github.com/boomboxOff/WinPiBooster/releases/latest) et le placer dans le dossier `WinPiBooster\`

Le dossier doit ressembler à ceci :
```
WinPiBooster\
  WinPiBooster.exe   ← téléchargé depuis la release
  WinPiBooster.bat
  setup.bat
  README.md
  go.mod
  main.go
  updates.go
  reports.go
  cli.go
  logger.go
  service.go
  config.go
```

## Lancement

Double-cliquer sur `WinPiBooster.bat` — l'élévation des droits administrateur est demandée automatiquement.

Ou depuis un terminal en administrateur :

```bat
WinPiBooster.exe
```

## Commandes CLI

| Commande | Description |
|---|---|
| `WinPiBooster.exe` | Mode interactif (console, Ctrl+C pour quitter) |
| `WinPiBooster.exe --dry-run` | Vérifie les mises à jour disponibles sans les installer |
| `WinPiBooster.exe check` | Alias de `--dry-run` (mêmes codes de sortie : 0/1/2) |
| `WinPiBooster.exe install` | Installe le service Windows (démarrage automatique) |
| `WinPiBooster.exe start` | Démarre le service |
| `WinPiBooster.exe stop` | Arrête le service |
| `WinPiBooster.exe remove` | Désinstalle le service |
| `WinPiBooster.exe status` | Affiche l'état du service, la configuration et le dernier cycle |
| `WinPiBooster.exe diagnose` | Vérifie les prérequis et affiche un rapport de santé |
| `WinPiBooster.exe clean-logs` | Supprime les archives de logs expirées |
| `WinPiBooster.exe list-logs` | Liste tous les fichiers de log avec taille et date |
| `WinPiBooster.exe tail` | Affiche les 20 dernières lignes du log (`--lines N` pour changer) |
| `WinPiBooster.exe history` | Liste toutes les mises à jour installées (logs courant + archives) |
| `WinPiBooster.exe history --since DATE` | Filtre les installations depuis `DATE` (format `YYYY-MM-DD`) |
| `WinPiBooster.exe logs` | Ouvre `UpdateLog.txt` dans le Bloc-notes |
| `WinPiBooster.exe report` | Affiche les compteurs courants (sans reset) |
| `WinPiBooster.exe reset-counters` | Remet les compteurs à zéro et réécrit status.json |
| `WinPiBooster.exe show-config` | Affiche la configuration active |
| `WinPiBooster.exe show-config --json` | Affiche la configuration au format JSON |
| `WinPiBooster.exe export-config` | Écrit `config.json` depuis la configuration active (`--force` pour écraser) |
| `WinPiBooster.exe install --start` | Installe ET démarre le service en une seule commande |
| `WinPiBooster.exe version` | Affiche la version |
| `WinPiBooster.exe --version` | Alias Unix pour `version` |
| `WinPiBooster.exe help` | Affiche l'aide complète |

### Codes de sortie

| Code | Commande | Signification |
|---|---|---|
| `0` | toutes | Succès |
| `1` | toutes | Erreur |
| `2` | `--dry-run` | Des mises à jour sont disponibles |
| `1` | `diagnose` | Au moins un prérequis manquant |

## Auto-démarrage avec Windows (service natif)

Pour installer WinPiBooster comme service Windows (démarrage automatique, redémarrage sur crash) :

1. Double-cliquer sur `setup.bat`
2. Accepter l'élévation UAC

Ou depuis un terminal en administrateur :

```bat
WinPiBooster.exe install   # installe le service
WinPiBooster.exe start     # démarre le service
WinPiBooster.exe stop      # arrête le service
WinPiBooster.exe remove    # désinstalle le service
```

Le service est visible dans `services.msc` et gérable via `sc.exe`.

## Configuration (`config.json`)

Créer `config.json` dans le même répertoire que `WinPiBooster.exe`. Toutes les clés sont optionnelles.

```json
{
  "check_interval_seconds": 60,
  "retry_attempts": 3,
  "log_retention_days": 30,
  "max_log_size_mb": 10,
  "ps_timeout_minutes": 10,
  "cmd_timeout_seconds": 300,
  "log_level": "info",
  "notifications_enabled": true,
  "min_free_disk_mb": 500,
  "heartbeat_interval_minutes": 60,
  "retry_delay_seconds": 5
}
```

| Clé | Défaut | Description |
|---|---|---|
| `check_interval_seconds` | `60` | Intervalle entre deux vérifications de mises à jour |
| `retry_attempts` | `3` | Nombre de tentatives sur chaque opération critique |
| `log_retention_days` | `30` | Durée de conservation des archives de logs (jours) |
| `max_log_size_mb` | `10` | Taille maximale de UpdateLog.txt avant rotation (MB) |
| `ps_timeout_minutes` | `10` | Timeout des commandes PowerShell (minutes) |
| `cmd_timeout_seconds` | `300` | Timeout des commandes système (secondes) |
| `log_level` | `"info"` | Niveau de log : `debug`, `info`, `warn`, `error` (remplace `DEBUG=true`) |
| `notifications_enabled` | `true` | Activer/désactiver les notifications toast Windows |
| `min_free_disk_mb` | `500` | Espace disque minimum requis sur C: avant installation (MB) |
| `heartbeat_interval_minutes` | `60` | Intervalle entre deux heartbeats (minutes, minimum 5) |
| `retry_delay_seconds` | `5` | Délai de base entre tentatives (`×3`, `×6` pour les suivantes) |

## Logs

Les logs sont écrits dans `UpdateLog.txt` et archivés sous la forme `UpdateLog_<timestamp>.txt` :
- à chaque lancement
- automatiquement à **minuit** (avant le rapport quotidien)
- automatiquement quand `UpdateLog.txt` dépasse la taille limite (défaut 10 MB)

Les archives de plus de 30 jours sont supprimées automatiquement.

**Format fichier** (plain text) :
```
2026-02-24 10:00:00 [INFO]: ──────────────────────────────────────────────────────────────
2026-02-24 10:00:00 [INFO]: WinPiBooster v2.19.0 — actif depuis 0m 0s | vérifications: 0 | installées: 0 | erreurs: 0
2026-02-24 10:34:00 [INFO]: Mise à jour disponible : KB5034441
```

**Console** : même format avec couleurs par niveau (INFO vert, WARN jaune, ERROR rouge).

### Mode debug

Via config.json (recommandé) :
```json
{ "log_level": "debug" }
```

Ou via variable d'environnement (compatibilité) :
```bat
SET DEBUG=true
WinPiBooster.exe
```

## status.json

Après chaque cycle réussi, WinPiBooster écrit `status.json` dans le répertoire de l'exécutable :

```json
{
  "version": "v2.19.0",
  "last_check": "2026-02-24T10:15:00Z",
  "next_check": "2026-02-24T10:16:00Z",
  "uptime_seconds": 3600,
  "updates_checked": 10,
  "updates_installed": 3,
  "updates_skipped": 6,
  "cycle_errors": 1,
  "last_installed": [
    {
      "kb": "KB5034441",
      "title": "2024-01 Cumulative Update for Windows 11",
      "installed_at": "2026-02-24T10:15:00Z"
    }
  ],
  "last_error": ""
}
```

Ce fichier peut être lu par des outils de monitoring externes.

## CI (GitHub Actions)

Le pipeline CI s'exécute sur `windows-latest` à chaque push sur `master` :

1. `go mod tidy` — vérifie la cohérence du module
2. `go vet` — analyse statique de base
3. `staticcheck` — analyse statique avancée
4. `go test -race -count=1 -timeout 120s` — tests unitaires avec détecteur de races
5. `go test -count=1 -timeout 120s -coverprofile` — couverture de code (seuil minimum : **40%**)
6. `go build` — compilation du binaire final

## Build depuis les sources

Prérequis : [Go 1.22+](https://go.dev/dl/)

```bat
go build -ldflags="-s -w -X main.version=v2.19.0" -o WinPiBooster.exe .
```
