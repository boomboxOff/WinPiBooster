# WinPiBooster

![CI](https://github.com/boomboxOff/WinPiBooster/actions/workflows/ci.yml/badge.svg)

Binaire Windows de surveillance et d'installation automatique des mises à jour Windows.

## Fonctionnement

- Vérifie toutes les **60 secondes** si des mises à jour Windows sont disponibles
- Installe automatiquement les mises à jour détectées avec redémarrage automatique si nécessaire
- Envoie des **notifications Windows toast** à chaque événement clé (démarrage, arrêt, installation, erreur, rapport)
- Génère un **rapport quotidien à minuit** avec remise à zéro des compteurs
- Archive les logs à chaque lancement, lors d'un dépassement de taille (**10 MB** par défaut) et supprime les archives de plus de **30 jours**
- Envoie un **heartbeat toutes les heures** avec uptime et compteurs
- **Circuit breaker** : pause automatique en cas d'erreurs répétées (seuil configurable)
- **Instance unique** : empêche le lancement de plusieurs instances interactives simultanées
- Écrit un fichier **status.json** après chaque cycle réussi

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
| `WinPiBooster.exe install` | Installe le service Windows (démarrage automatique) |
| `WinPiBooster.exe start` | Démarre le service |
| `WinPiBooster.exe stop` | Arrête le service |
| `WinPiBooster.exe remove` | Désinstalle le service |
| `WinPiBooster.exe status` | Affiche l'état du service, la configuration et le dernier cycle |
| `WinPiBooster.exe clean-logs` | Supprime les archives de logs expirées |
| `WinPiBooster.exe list-logs` | Liste tous les fichiers de log avec taille et date |
| `WinPiBooster.exe logs` | Ouvre `UpdateLog.txt` dans le Bloc-notes |
| `WinPiBooster.exe report` | Affiche les compteurs courants (sans reset) |
| `WinPiBooster.exe version` | Affiche la version |
| `WinPiBooster.exe help` | Affiche l'aide complète |

### Codes de sortie (`--dry-run`)

| Code | Signification |
|---|---|
| `0` | Aucune mise à jour disponible |
| `1` | Erreur |
| `2` | Des mises à jour sont disponibles |

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
  "circuit_breaker_threshold": 5,
  "circuit_breaker_pause_minutes": 30
}
```

| Clé | Défaut | Description |
|---|---|---|
| `check_interval_seconds` | 60 | Intervalle entre deux vérifications de mises à jour |
| `retry_attempts` | 3 | Nombre de tentatives sur chaque opération critique |
| `log_retention_days` | 30 | Durée de conservation des archives de logs (jours) |
| `max_log_size_mb` | 10 | Taille maximale de UpdateLog.txt avant rotation (MB) |
| `ps_timeout_minutes` | 10 | Timeout des commandes PowerShell (minutes) |
| `cmd_timeout_seconds` | 300 | Timeout des commandes système (secondes) |
| `circuit_breaker_threshold` | 5 | Nombre d'erreurs consécutives avant déclenchement du circuit breaker |
| `circuit_breaker_pause_minutes` | 30 | Durée de la pause du circuit breaker (minutes) |

## Logs

Les logs sont écrits dans `UpdateLog.txt` et archivés sous la forme `UpdateLog_<timestamp>.txt` :
- à chaque lancement
- automatiquement quand `UpdateLog.txt` dépasse la taille limite (défaut 10 MB)

Les archives de plus de 30 jours sont supprimées automatiquement.

**Format fichier** (plain text) :
```
2026-02-23 10:00:00 [INFO]: ──────────────────────────────────────────────────────────────
2026-02-23 10:00:00 [INFO]: Script actif — surveillance des mises à jour Windows en cours.
2026-02-23 10:34:00 [INFO]: Mise à jour disponible : KB5034441
```

**Console** : même format avec couleurs par niveau (INFO vert, WARN jaune, ERROR rouge).

### Mode debug

Pour activer les logs verbeux :

```bat
SET DEBUG=true
WinPiBooster.exe
```

## status.json

Après chaque cycle réussi, WinPiBooster écrit `status.json` dans le répertoire de l'exécutable :

```json
{
  "version": "v2.7.0",
  "last_check": "2026-02-23T20:15:00Z",
  "updates_checked": 10,
  "updates_installed": 3,
  "updates_skipped": 6,
  "cycle_errors": 1
}
```

Ce fichier peut être lu par des outils de monitoring externes.

## CI (GitHub Actions)

Le pipeline CI s'exécute sur `windows-latest` à chaque push sur `master` :

1. `go mod tidy` — vérifie la cohérence du module
2. `go vet` — analyse statique de base
3. `staticcheck` — analyse statique avancée
4. `go test -race` — tests unitaires avec détecteur de races
5. `go test -coverprofile` — couverture de code (seuil minimum : 15%)
6. `go build` — compilation du binaire final

## Build depuis les sources

Prérequis : [Go 1.22+](https://go.dev/dl/)

```bat
go build -ldflags="-s -w -X main.version=v2.7.0" -o WinPiBooster.exe .
```
