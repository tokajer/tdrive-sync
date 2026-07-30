/*
    Shared state resolution for the TDrive Sync Dolphin integration.

    SPDX-License-Identifier: MIT
*/

#include "tdrivestate.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QStringList>

#include <algorithm>

#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

// SEEK_HOLE needs _GNU_SOURCE on glibc; define it if the headers kept quiet.
#ifndef SEEK_HOLE
#define SEEK_HOLE 4
#endif

namespace TDrive
{

/** Format version of the published file; anything else is ignored. */
static const int kStateVersion = 1;

QString statePath()
{
    QString base = qEnvironmentVariable("XDG_STATE_HOME");
    if (base.isEmpty()) {
        base = QDir::homePath() + QLatin1String("/.local/state");
    }
    return base + QLatin1String("/tdrive-sync/file-manager.json");
}

Info loadInfo()
{
    Info info;
    QFile f(statePath());
    if (!f.open(QIODevice::ReadOnly)) {
        return info;
    }
    const QJsonDocument doc = QJsonDocument::fromJson(f.readAll());
    if (!doc.isObject()) {
        return info;
    }
    const QJsonObject o = doc.object();
    if (o.value(QLatin1String("version")).toInt() != kStateVersion) {
        return info;
    }
    info.active = o.value(QLatin1String("active")).toBool();
    info.mode = o.value(QLatin1String("mode")).toString();
    info.state = o.value(QLatin1String("state")).toString();
    info.root = o.value(QLatin1String("root")).toString();
    info.cacheDir = o.value(QLatin1String("cache_dir")).toString();
    info.remote = o.value(QLatin1String("remote")).toString();
    info.exec = o.value(QLatin1String("exec")).toString();
    while (info.root.size() > 1 && info.root.endsWith(QLatin1Char('/'))) {
        info.root.chop(1);
    }
    const QJsonArray pinned = o.value(QLatin1String("pinned")).toArray();
    for (const QJsonValue &v : pinned) {
        const QString p = v.toString();
        if (!p.isEmpty()) {
            info.pinned << p;
        }
    }
    return info;
}

QString relativePath(const Info &info, const QString &localFile)
{
    if (info.root.isEmpty() || localFile.isEmpty()) {
        return QString();
    }
    const QString prefix = info.root + QLatin1Char('/');
    if (!localFile.startsWith(prefix)) {
        return QString();
    }
    QString rel = localFile.mid(prefix.size());
    while (rel.endsWith(QLatin1Char('/'))) {
        rel.chop(1);
    }
    return rel;
}

bool isPinned(const Info &info, const QString &rel)
{
    for (const QString &p : info.pinned) {
        if (rel == p || rel.startsWith(p + QLatin1Char('/'))) {
            return true;
        }
    }
    return false;
}

/** rclone's cache metadata for one file, as far as we use it. */
struct Meta {
    bool found = false;
    bool dirty = false;
    qint64 size = 0;
    /** Whether the recorded byte ranges span the whole file. */
    bool covers(qint64 whole) const;

    struct Range {
        qint64 pos;
        qint64 end;
    };
    QList<Range> ranges;
};

bool Meta::covers(qint64 whole) const
{
    if (ranges.isEmpty()) {
        return false;
    }
    QList<Range> sorted = ranges;
    std::sort(sorted.begin(), sorted.end(), [](const Range &a, const Range &b) {
        return a.pos < b.pos;
    });
    qint64 reached = 0;
    for (const Range &r : sorted) {
        if (r.pos > reached) {
            return false; // gap
        }
        reached = std::max(reached, r.end);
    }
    return reached >= whole;
}

static Meta readMeta(const QString &metaFile)
{
    Meta m;
    QFile f(metaFile);
    if (!f.open(QIODevice::ReadOnly)) {
        return m;
    }
    const QJsonDocument doc = QJsonDocument::fromJson(f.readAll());
    if (!doc.isObject()) {
        return m;
    }
    m.found = true;
    const QJsonObject o = doc.object();
    m.dirty = o.value(QLatin1String("Dirty")).toBool();
    m.size = static_cast<qint64>(o.value(QLatin1String("Size")).toDouble());
    const QJsonArray rs = o.value(QLatin1String("Rs")).toArray();
    for (const QJsonValue &v : rs) {
        const QJsonObject r = v.toObject();
        const qint64 pos = static_cast<qint64>(r.value(QLatin1String("Pos")).toDouble());
        const qint64 len = static_cast<qint64>(r.value(QLatin1String("Size")).toDouble());
        m.ranges.append({pos, pos + len});
    }
    return m;
}

/** Offset of the first not-yet-downloaded region, or whole when there is none. */
static qint64 firstHole(const QByteArray &nativePath, qint64 whole)
{
    const int fd = ::open(nativePath.constData(), O_RDONLY | O_CLOEXEC);
    if (fd < 0) {
        return whole;
    }
    const off_t off = ::lseek(fd, 0, SEEK_HOLE);
    ::close(fd);
    if (off < 0) {
        return whole; // no sparse-file support: assume it is all there
    }
    return static_cast<qint64>(off);
}

/**
 * Whether a cache directory holds any downloaded data.
 *
 * The directory tree outlives the data in it: rclone creates the folders when it
 * first touches a file below them, freeing a single file leaves its parents
 * behind, and a file that was only opened stays a fully sparse placeholder.
 * Taking "the cache folder exists" for "something is local" would leave folders
 * marked as partially offline long after the last byte was freed.
 *
 * Bounded on purpose: this runs from getOverlays() for every visible item, so
 * after kScanBudget entries it gives up and assumes there is data. A folder that
 * really holds some hits the first entry anyway.
 */
static bool dirHasData(const QString &dir)
{
    static const int kScanBudget = 64;
    int budget = kScanBudget;
    QStringList queue{dir};
    while (!queue.isEmpty()) {
        const QString cur = queue.takeFirst();
        const QDir d(cur);
        if (!d.exists()) {
            continue;
        }
        const QFileInfoList entries =
            d.entryInfoList(QDir::Files | QDir::Dirs | QDir::NoDotAndDotDot | QDir::Hidden | QDir::System);
        for (const QFileInfo &e : entries) {
            if (budget <= 0) {
                return true; // out of budget: keep the answer we gave before
            }
            --budget;
            if (e.isDir()) {
                queue.append(e.absoluteFilePath());
                continue;
            }
            struct stat st;
            if (::stat(QFile::encodeName(e.absoluteFilePath()).constData(), &st) != 0) {
                continue;
            }
            if (st.st_size > 0 && st.st_blocks == 0) {
                continue; // a placeholder rclone has not downloaded into yet
            }
            return true;
        }
    }
    return false;
}

/** What the local cache says about one file. */
struct CacheInfo {
    bool found = false; /**< there is a cache file at all */
    bool empty = false; /**< it exists but holds no data yet */
    bool dirty = false; /**< it holds local changes not sent to Drive yet */
    bool complete = false; /**< the whole file is there */
};

/**
 * Completeness cannot be read off rclone's recorded ranges alone: rclone leaves
 * "Rs" empty both for a file it has fully downloaded and for one it has barely
 * touched. What is reliable is how much of the sparse cache file is actually
 * allocated, with the first hole as the tie-breaker (which also survives a
 * filesystem with transparent compression).
 */
static CacheInfo inspect(const QString &dataFile, const QString &metaFile)
{
    CacheInfo c;
    const QByteArray native = QFile::encodeName(dataFile);
    struct stat st;
    if (::stat(native.constData(), &st) != 0 || S_ISDIR(st.st_mode)) {
        return c;
    }
    c.found = true;

    qint64 size = static_cast<qint64>(st.st_size);
    const Meta m = readMeta(metaFile);
    if (m.found) {
        c.dirty = m.dirty;
        if (m.size > 0) {
            size = m.size;
        }
    }
    if (size <= 0) {
        c.complete = true;
        return c;
    }

    const qint64 allocated = static_cast<qint64>(st.st_blocks) * 512;
    if (m.covers(size) || allocated >= size) {
        c.complete = true;
    } else if (allocated == 0) {
        c.empty = true;
    } else {
        const qint64 hole = firstHole(native, size);
        if (hole >= size) {
            c.complete = true;
        } else if (hole == 0) {
            c.empty = true;
        }
    }
    return c;
}

State resolve(const Info &info, const QString &localFile)
{
    if (!info.usable()) {
        return State::Unknown;
    }
    const QString rel = relativePath(info, localFile);
    if (rel.isEmpty()) {
        return State::Unknown;
    }
    if (info.mode == QLatin1String("mirror")) {
        return State::Local;
    }

    const bool pinned = isPinned(info, rel);
    const QString cacheBase = info.cacheDir + QLatin1Char('/');
    const QString dataFile = cacheBase + QLatin1String("vfs/") + info.remote + QLatin1Char('/') + rel;
    const QString metaFile = cacheBase + QLatin1String("vfsMeta/") + info.remote + QLatin1Char('/') + rel;

    // Folders are recognised by their cache directory, so nothing here has to
    // stat the FUSE mount – a stat on an unresponsive mount would freeze the
    // file manager. A folder counts as partially available as soon as anything
    // below it is cached: deciding "all of it is local" would mean walking the
    // whole subtree on every single lookup.
    if (QFileInfo(dataFile).isDir()) {
        if (pinned) {
            return State::Pinned;
        }
        return dirHasData(dataFile) ? State::Partial : State::Cloud;
    }

    const CacheInfo c = inspect(dataFile, metaFile);
    if (!c.found || c.empty) {
        return pinned ? State::Pinning : State::Cloud;
    }
    if (c.dirty) {
        return State::Uploading;
    }
    if (!c.complete) {
        return pinned ? State::Pinning : State::Partial;
    }
    return pinned ? State::Pinned : State::Cached;
}

QString overlayIcon(State state)
{
    // Icons from the Breeze theme, chosen to stay apart at overlay size: a
    // download arrow means "not here yet", a green check means "here", a star
    // means "kept here on purpose".
    switch (state) {
    case State::Cloud:
        return QStringLiteral("cloud-download");
    case State::Partial:
        return QStringLiteral("vcs-update-required");
    case State::Cached:
    case State::Local:
        return QStringLiteral("vcs-normal");
    case State::Pinned:
        return QStringLiteral("emblem-favorite");
    case State::Pinning:
        return QStringLiteral("emblem-synchronizing-symbolic");
    case State::Uploading:
        return QStringLiteral("cloud-upload");
    case State::Unknown:
        break;
    }
    return QString();
}

QString text(const char *english, const char *german)
{
    // Same rule as the Go side (internal/i18n): the first locale variable that
    // is set decides, and anything that is not German falls back to English.
    for (const char *var : {"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"}) {
        const QString value = qEnvironmentVariable(var);
        if (value.isEmpty()) {
            continue;
        }
        return value.startsWith(QLatin1String("de"), Qt::CaseInsensitive) ? QString::fromUtf8(german)
                                                                         : QString::fromUtf8(english);
    }
    return QString::fromUtf8(english);
}

QString stateLabel(State state)
{
    switch (state) {
    case State::Cloud:
        return text("Online only – downloaded when opened", "Nur online – wird beim Öffnen geladen");
    case State::Partial:
        return text("Partially available offline", "Teilweise offline verfügbar");
    case State::Cached:
        return text("Available offline", "Offline verfügbar");
    case State::Pinned:
        return text("Always kept offline", "Dauerhaft offline verfügbar");
    case State::Pinning:
        return text("Making available offline…", "Wird offline verfügbar gemacht…");
    case State::Uploading:
        return text("Uploading changes…", "Änderungen werden hochgeladen…");
    case State::Local:
        return text("Local copy (mirror mode)", "Lokale Kopie (Spiegel-Modus)");
    case State::Unknown:
        break;
    }
    return QString();
}

}
