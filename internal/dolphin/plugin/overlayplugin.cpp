/*
    Overlay icons for the TDrive Sync folder in Dolphin: shows per file whether
    it is streamed from Google Drive or available offline.

    SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
    SPDX-License-Identifier: GPL-3.0-or-later
*/

#include "tdrivestate.h"

#include <KOverlayIconPlugin>

#include <QDateTime>
#include <QFileInfo>
#include <QFileSystemWatcher>
#include <QHash>
#include <QList>
#include <QPair>
#include <QTimer>
#include <QUrl>

namespace
{
/** How long a resolved state may be reused (Dolphin asks in bursts). */
constexpr qint64 kResolveCacheMs = 800;
/** How often tracked items are re-checked for state changes. */
constexpr int kPollMs = 4000;
/** Items nobody asked about for this long are dropped (they left the view). */
constexpr qint64 kForgetMs = 120000;
/** Upper bound on tracked items, so a huge folder cannot grow the poll cost. */
constexpr int kMaxTracked = 2000;
/** Minimum spacing between attempts to (re)read a missing published state. */
constexpr qint64 kInfoRetryMs = 2000;
}

/**
 * Shows a OneDrive-style indicator on every item inside the TDrive Sync folder.
 *
 * getOverlays() must be fast and must not block: it resolves the state from
 * rclone's on-disk cache only, never from the FUSE mount, and reuses recent
 * results. Changes that happen behind the file manager's back (a download
 * finishing, a folder being pinned) are picked up by a timer and reported via
 * overlaysChanged().
 */
class TDriveSyncOverlayPlugin : public KOverlayIconPlugin
{
    Q_PLUGIN_METADATA(IID "org.kde.overlayicon.tdrivesync" FILE "tdrivesyncoverlay.json")
    Q_OBJECT

public:
    explicit TDriveSyncOverlayPlugin(QObject *parent = nullptr);

    QStringList getOverlays(const QUrl &url) override;

private:
    void reloadInfo();
    void watchStateFile();
    void poll();
    static QStringList iconsFor(TDrive::State state);

    TDrive::Info m_info;
    qint64 m_infoReadAt = 0;
    QFileSystemWatcher m_watcher;
    QTimer m_timer;

    struct Entry {
        TDrive::State state = TDrive::State::Unknown;
        qint64 resolvedAt = 0;
        qint64 askedAt = 0;
    };
    QHash<QString, Entry> m_seen;
};

TDriveSyncOverlayPlugin::TDriveSyncOverlayPlugin(QObject *parent)
    : KOverlayIconPlugin(parent)
{
    reloadInfo();
    watchStateFile();

    // The published file changes when the mode, the daemon state or the set of
    // pinned folders changes – all of which move indicators.
    const auto onStateFileChanged = [this]() {
        reloadInfo();
        watchStateFile(); // the file is replaced atomically, so re-arm the watch
        poll();
    };
    connect(&m_watcher, &QFileSystemWatcher::fileChanged, this, onStateFileChanged);
    connect(&m_watcher, &QFileSystemWatcher::directoryChanged, this, onStateFileChanged);

    m_timer.setInterval(kPollMs);
    connect(&m_timer, &QTimer::timeout, this, &TDriveSyncOverlayPlugin::poll);
}

void TDriveSyncOverlayPlugin::reloadInfo()
{
    m_info = TDrive::loadInfo();
    m_infoReadAt = QDateTime::currentMSecsSinceEpoch();
}

void TDriveSyncOverlayPlugin::watchStateFile()
{
    const QString path = TDrive::statePath();
    const QString dir = QFileInfo(path).absolutePath();
    if (!m_watcher.directories().contains(dir)) {
        m_watcher.addPath(dir);
    }
    if (!m_watcher.files().contains(path) && QFileInfo::exists(path)) {
        m_watcher.addPath(path);
    }
}

QStringList TDriveSyncOverlayPlugin::iconsFor(TDrive::State state)
{
    const QString icon = TDrive::overlayIcon(state);
    return icon.isEmpty() ? QStringList() : QStringList{icon};
}

QStringList TDriveSyncOverlayPlugin::getOverlays(const QUrl &url)
{
    if (!url.isLocalFile()) {
        return QStringList();
    }
    QString path = url.toLocalFile();
    while (path.size() > 1 && path.endsWith(QLatin1Char('/'))) {
        path.chop(1);
    }
    const qint64 now = QDateTime::currentMSecsSinceEpoch();

    // Without a usable snapshot there is nothing to show – but the daemon may
    // have started since we last looked, so retry occasionally.
    if (!m_info.usable()) {
        if (now - m_infoReadAt < kInfoRetryMs) {
            return QStringList();
        }
        reloadInfo();
        watchStateFile();
        if (!m_info.usable()) {
            return QStringList();
        }
    }

    auto it = m_seen.find(path);
    if (it != m_seen.end()) {
        it->askedAt = now;
        if (now - it->resolvedAt < kResolveCacheMs) {
            return iconsFor(it->state);
        }
        it->state = TDrive::resolve(m_info, path);
        it->resolvedAt = now;
        return iconsFor(it->state);
    }

    const TDrive::State state = TDrive::resolve(m_info, path);
    if (state == TDrive::State::Unknown) {
        return QStringList(); // outside the sync folder: not ours, stay cheap
    }
    if (m_seen.size() < kMaxTracked) {
        m_seen.insert(path, Entry{state, now, now});
        if (!m_timer.isActive()) {
            m_timer.start();
        }
    }
    return iconsFor(state);
}

void TDriveSyncOverlayPlugin::poll()
{
    reloadInfo(); // cheap, and covers a watcher notification we may have missed

    const qint64 now = QDateTime::currentMSecsSinceEpoch();
    QList<QPair<QString, TDrive::State>> changed;
    for (auto it = m_seen.begin(); it != m_seen.end();) {
        if (now - it->askedAt > kForgetMs) {
            it = m_seen.erase(it);
            continue;
        }
        const TDrive::State state = TDrive::resolve(m_info, it.key());
        it->resolvedAt = now;
        if (state != it->state) {
            it->state = state;
            changed.append({it.key(), state});
        }
        ++it;
    }
    if (m_seen.isEmpty()) {
        m_timer.stop();
    }
    // Emit outside the loop: a receiver may call back into getOverlays() and
    // invalidate the iterator.
    for (const auto &c : changed) {
        Q_EMIT overlaysChanged(QUrl::fromLocalFile(c.first), iconsFor(c.second));
    }
}

#include "overlayplugin.moc"
