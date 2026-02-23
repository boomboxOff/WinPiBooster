# WinPiBooster

![CI](https://github.com/boomboxOff/WinPiBooster/actions/workflows/ci.yml/badge.svg)

Binaire Windows de surveillance et d'installation automatique des mises à jour Windows pour un nœud Pi Network.

## Fonctionnement

- Vérifie toutes les **60 secondes** si des mises à jour Windows sont disponibles
- Installe automatiquement les mises à jour détectées avec redémarrage automatique si nécessaire
- Envoie des **notifications Windows toast** à chaque événement clé (installation, erreur, rapport)
- Génère un **rapport quotidien à minuit** avec remise à zéro des compteurs
- Archive les logs à chaque lancement, lors d'un dépassement de taille (**10 MB** par défaut) et supprime les archives de plus de **30 jours**
- Envoie un **heartbeat toutes les heures** dans les logs pour confirmer que le script est actif

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
```

## Lancement

Double-cliquer sur `WinPiBooster.bat` — l'élévation des droits administrateur est demandée automatiquement.

Ou depuis un terminal en administrateur :

```bat
WinPiBooster.exe
```

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

## Logs

Les logs sont écrits dans `UpdateLog.txt` et archivés sous la forme `UpdateLog_<timestamp>.txt` :
- à chaque lancement
- automatiquement quand `UpdateLog.txt` dépasse la taille limite (défaut 10 MB)

Les archives de plus de 30 jours sont supprimées automatiquement.

### Configuration optionnelle

Créer `config.json` dans le même répertoire que `WinPiBooster.exe` :

```json
{
  "check_interval_seconds": 60,
  "retry_attempts": 3,
  "log_retention_days": 30,
  "max_log_size_mb": 10
}
```

Toutes les clés sont optionnelles — les valeurs manquantes utilisent les valeurs par défaut ci-dessus.

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

## Build depuis les sources

Prérequis : [Go 1.22+](https://go.dev/dl/)

```bat
go build -ldflags="-s -w -X main.version=v2.3.0" -o WinPiBooster.exe .
```
