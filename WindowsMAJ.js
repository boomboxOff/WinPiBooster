const { exec } = require('child_process');
const { createLogger, transports, format } = require('winston');
const notifier = require('node-notifier');
const path = require('path');
const fs = require('fs');

// Logger setup
const logger = createLogger({
    level: 'info',
    format: format.combine(
        format.timestamp({ format: 'YYYY-MM-DD HH:mm:ss' }),
        format.printf(({ timestamp, level, message }) => `${timestamp} [${level.toUpperCase()}]: ${message}`)
    ),
    transports: [
        new transports.File({ filename: path.join(__dirname, 'UpdateLog.txt') }),
        new transports.Console()
    ],
});

let updatesChecked = 0;
let updatesInstalled = 0;
let updatesSkipped = 0;
let isRunning = false;

// Helper function to execute shell commands
async function execCommand(command) {
    return new Promise((resolve, reject) => {
        exec(command, { maxBuffer: 10 * 1024 * 1024, encoding: 'utf8' }, (error, stdout, stderr) => {
            if (error) reject(stderr || error.message);
            else resolve(stdout.trim());
        });
    });
}

// Show Windows notification
function showNotification(title, message) {
    notifier.notify({
        title: title,
        message: message,
        sound: true,
        wait: false
    });
}

// Install NuGet provider if not already installed
async function installNuGetProvider() {
    try {
        logger.info("Vérification et installation du fournisseur NuGet...");
        await execCommand('powershell.exe -Command "Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Confirm:$false"');
        logger.info("Fournisseur NuGet installé avec succès.");
    } catch (error) {
        logger.warn(`Le fournisseur NuGet est peut-être déjà installé : ${error}`);
    }
}

// Check if the PSWindowsUpdate module is installed
async function isPSWindowsUpdateModuleInstalled() {
    try {
        const result = await execCommand('powershell.exe -Command "Get-Module -ListAvailable -Name PSWindowsUpdate"');
        return result.includes('PSWindowsUpdate');
    } catch (error) {
        logger.error(`Erreur lors de la vérification du module PSWindowsUpdate : ${error}`);
        return false;
    }
}

// Install the PSWindowsUpdate module if not installed
async function installPSWindowsUpdateModule() {
    if (await isPSWindowsUpdateModuleInstalled()) {
        logger.info("Le module PSWindowsUpdate est déjà installé.");
    } else {
        try {
            // Install NuGet provider first to avoid interactive prompts
            await installNuGetProvider();

            logger.info("Installation du module PSWindowsUpdate...");
            const result = await execCommand('powershell.exe -Command "Install-Module -Name PSWindowsUpdate -Force -SkipPublisherCheck -Confirm:$false -AllowClobber"');
            if (result.toLowerCase().includes('error')) {
                logger.error("Erreur détectée pendant l'installation : Conflit potentiel avec les politiques de sécurité ou les permissions administratives.");
                showNotification("Erreur", "Installation du module PSWindowsUpdate échouée.");
            } else {
                logger.info("Module PSWindowsUpdate installé avec succès.");
                showNotification("Succès", "Module PSWindowsUpdate installé.");
            }
        } catch (error) {
            logger.error(`Erreur lors de l'installation du module PSWindowsUpdate : ${error}`);
            showNotification("Erreur", "Erreur lors de l'installation du module PSWindowsUpdate.");
            throw error;
        }
    }
}

// Ensure the Windows Update service is running
async function ensureWindowsUpdateServiceRunning() {
    try {
        const result = await execCommand('sc query wuauserv');
        if (result.includes('STATE              : 4  RUNNING')) {
            logger.info("Le service Windows Update est déjà en cours d'exécution.");
        } else {
            logger.info("Démarrage du service Windows Update...");
            await execCommand('sc start wuauserv');
            logger.info("Service Windows Update démarré.");
        }
    } catch (error) {
        logger.error(`Erreur lors du démarrage du service Windows Update : ${error}`);
        throw error;
    }
}

// Check available updates
async function checkAvailableUpdates() {
    try {
        logger.info("Vérification des mises à jour disponibles...");
        const updatesRaw = await execCommand('powershell.exe -Command "Get-WindowsUpdate -MicrosoftUpdate | ConvertTo-Json -Compress"');
        if (!updatesRaw) {
            logger.info("Aucune donnée retournée par PowerShell. Aucune mise à jour disponible ou problème détecté.");
            updatesSkipped++;
            return [];
        }

        const updates = JSON.parse(updatesRaw);

        if (Array.isArray(updates) && updates.length > 0) {
            updates.forEach(update => {
                logger.info(`Mise à jour disponible :
  - Titre : ${update.Title}
  - KB : ${update.KBArticleIDs}
  - Taille : ${update.Size}
  - Ordinateur : ${update.PSComputerName}`);
            });
            updatesChecked += updates.length;
            return updates;
        } else {
            logger.info("Aucune mise à jour disponible.");
            updatesSkipped++;
            return [];
        }
    } catch (error) {
        logger.error(`Erreur lors de la vérification des mises à jour : ${error}`);
        updatesSkipped++;
        return [];
    }
}

// Install updates
async function installUpdates(updates) {
    try {
        logger.info("Installation des mises à jour...");
        const result = await execCommand('powershell.exe -Command "Install-WindowsUpdate -MicrosoftUpdate -AcceptAll -AutoReboot"');
        logger.info(`Résultat de l'installation des mises à jour :
${result}`);
        showNotification("Succès", `Mises à jour Windows installées : ${updates.map(u => u.Title).join(", ")}`);
        updatesInstalled += updates.length;
    } catch (error) {
        logger.error(`Erreur lors de l'installation des mises à jour : ${error}`);
        throw error;
    }
}

// Archive old logs and delete archives older than 30 days
function archiveOldLogs() {
    const logFile = path.join(__dirname, 'UpdateLog.txt');
    const archiveFile = path.join(__dirname, `UpdateLog_${new Date().toISOString().replace(/[:.]/g, '-')}.txt`);
    if (fs.existsSync(logFile)) {
        fs.renameSync(logFile, archiveFile);
        logger.info("Journal archivé.");
    }

    const maxAgeDays = 30;
    const cutoff = Date.now() - maxAgeDays * 24 * 60 * 60 * 1000;
    fs.readdirSync(__dirname)
        .filter(f => f.startsWith('UpdateLog_') && f.endsWith('.txt'))
        .forEach(f => {
            const filePath = path.join(__dirname, f);
            const { mtimeMs } = fs.statSync(filePath);
            if (mtimeMs < cutoff) {
                fs.unlinkSync(filePath);
                logger.info(`Ancien journal supprimé : ${f}`);
            }
        });
}

// Generate daily report
function generateDailyReport() {
    const report = `Rapport quotidien :
- Vérifications totales : ${updatesChecked}
- Mises à jour installées : ${updatesInstalled}
- Vérifications sans mise à jour : ${updatesSkipped}`;
    logger.info(report);
    showNotification("Rapport quotidien", report);
}

// Main function to orchestrate the update process
async function main() {
    if (isRunning) {
        logger.info("Cycle précédent toujours en cours, passage ignoré.");
        return;
    }
    isRunning = true;
    logger.info("Lancement du processus de mise à jour Windows...");
    archiveOldLogs();

    try {
        await installPSWindowsUpdateModule();
        await ensureWindowsUpdateServiceRunning();

        const updates = await checkAvailableUpdates();
        if (updates.length > 0) {
            await installUpdates(updates);
        }
    } catch (error) {
        logger.error(`Erreur globale du processus de mise à jour : ${error}`);
        showNotification("Erreur", "Erreur globale du processus de mise à jour.");
    } finally {
        isRunning = false;
        logger.info("Processus terminé.");
    }
}

// Schedule periodic updates
const checkInterval = 60 * 1000; // Vérification toutes les minutes
main();
setInterval(() => {
    logger.info("Début d'un nouveau cycle de vérification des mises à jour.");
    main();
}, checkInterval);

// Generate daily report at midnight
setInterval(() => {
    const now = new Date();
    if (now.getHours() === 0 && now.getMinutes() === 0) {
        generateDailyReport();
    }
}, 60 * 1000);
