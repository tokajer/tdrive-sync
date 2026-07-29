/*
    Context-menu actions for the TDrive Sync folder in Dolphin: pin files and
    folders for offline use, or release them back to online-only.

    SPDX-License-Identifier: MIT
*/

#include "tdrivestate.h"

#include <KAbstractFileItemActionPlugin>
#include <KFileItemListProperties>
#include <KPluginFactory>

#include <QAction>
#include <QIcon>
#include <QList>
#include <QProcess>
#include <QUrl>
#include <QWidget>

/**
 * Adds "Keep offline" / "Online only" to the context menu, but only for items
 * inside the TDrive Sync folder – so nothing shows up anywhere else.
 */
class TDriveSyncActionPlugin : public KAbstractFileItemActionPlugin
{
    Q_OBJECT

public:
    explicit TDriveSyncActionPlugin(QObject *parent, const QVariantList &args);

    QList<QAction *> actions(const KFileItemListProperties &fileItemInfos, QWidget *parentWidget) override;

private:
    /** Runs the daemon's CLI for the given selection, detached. */
    void run(const QString &subcommand, const QStringList &paths);

    TDrive::Info m_info;
};

TDriveSyncActionPlugin::TDriveSyncActionPlugin(QObject *parent, const QVariantList &args)
    : KAbstractFileItemActionPlugin(parent)
{
    Q_UNUSED(args)
}

void TDriveSyncActionPlugin::run(const QString &subcommand, const QStringList &paths)
{
    if (m_info.exec.isEmpty() || paths.isEmpty()) {
        return;
    }
    QStringList args{QStringLiteral("offline"), subcommand};
    args += paths;
    QProcess::startDetached(m_info.exec, args);
}

QList<QAction *> TDriveSyncActionPlugin::actions(const KFileItemListProperties &fileItemInfos, QWidget *parentWidget)
{
    QList<QAction *> actions;
    m_info = TDrive::loadInfo();
    if (!m_info.usable() || m_info.mode != QLatin1String("stream") || m_info.exec.isEmpty()) {
        return actions; // nothing to pin in mirror mode: it is all local already
    }

    // Only act on the selection if every item really is inside the sync folder.
    QStringList paths;
    bool anyUnpinned = false;
    bool anyLocal = false;
    for (const QUrl &url : fileItemInfos.urlList()) {
        if (!url.isLocalFile()) {
            return actions;
        }
        const QString path = url.toLocalFile();
        const QString rel = TDrive::relativePath(m_info, path);
        if (rel.isEmpty()) {
            return actions;
        }
        if (TDrive::isPinned(m_info, rel)) {
            anyLocal = true; // releasing it frees whatever was downloaded
        } else {
            anyUnpinned = true;
        }
        switch (TDrive::resolve(m_info, path)) {
        case TDrive::State::Partial:
        case TDrive::State::Cached:
        case TDrive::State::Pinned:
        case TDrive::State::Pinning:
            anyLocal = true;
            break;
        default:
            break;
        }
        paths << path;
    }
    if (paths.isEmpty()) {
        return actions;
    }

    // Both entries can show up at once, as in the Windows OneDrive client:
    // pinning is about the future, freeing space is about what is already here.
    if (anyUnpinned) {
        auto *keep = new QAction(QIcon::fromTheme(QStringLiteral("emblem-favorite")),
                                 TDrive::text("Always keep offline", "Dauerhaft offline verfügbar machen"),
                                 parentWidget);
        connect(keep, &QAction::triggered, this, [this, paths]() {
            run(QStringLiteral("on"), paths);
        });
        actions << keep;
    }
    if (anyLocal) {
        auto *free = new QAction(QIcon::fromTheme(QStringLiteral("cloudstatus")),
                                 TDrive::text("Free up space (keep online only)",
                                              "Speicherplatz freigeben (nur online behalten)"),
                                 parentWidget);
        connect(free, &QAction::triggered, this, [this, paths]() {
            run(QStringLiteral("off"), paths);
        });
        actions << free;
    }
    return actions;
}

K_PLUGIN_CLASS_WITH_JSON(TDriveSyncActionPlugin, "tdrivesyncaction.json")

#include "actionplugin.moc"
