# MajWindowsJs

Script Node.js de surveillance et d'installation automatique des mises à jour Windows.

## Fonctionnement

- Vérifie toutes les **60 secondes** si des mises à jour Windows sont disponibles
- Installe automatiquement les mises à jour détectées
- Envoie des **notifications Windows** à chaque étape (installation, erreur, rapport)
- Archive les logs à chaque lancement et génère un **rapport quotidien à minuit**

## Prérequis

- Windows 10/11
- [Node.js](https://nodejs.org/) installé dans `C:\Program Files\nodejs\`
- PowerShell avec droits administrateur (requis pour Windows Update)

## Installation

```bash
git clone https://github.com/boomboxOff/MajWindowsJs.git
cd MajWindowsJs
npm install
```

## Lancement

Double-cliquer sur `WindowsMAJ.bat` ou lancer depuis un terminal **en administrateur** :

```bash
node WindowsMAJ.js
```

> Le script doit tourner avec des droits administrateur pour pouvoir interagir avec Windows Update.

## Dépendances

| Package | Rôle |
|---|---|
| `winston` | Logging fichier + console |
| `node-notifier` | Notifications Windows toast |

Le module PowerShell **PSWindowsUpdate** est installé automatiquement par le script s'il est absent.

## Logs

Les logs sont générés dans le dossier du script sous la forme `UpdateLog_<timestamp>.txt`. Chaque lancement archive le log précédent.
