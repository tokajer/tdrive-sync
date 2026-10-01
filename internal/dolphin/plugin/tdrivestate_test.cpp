/*
    Runs the shared state specification against the C++ implementation.

    The state logic exists twice on purpose - once in Go (internal/fmstate, used
    by the daemon and the CLI) and once here, because an overlay lookup runs for
    every visible item and must not go through any IPC. Prose alone kept them in
    step until now. This binary reads the same fixture the Go test does,
    internal/fmstate/testdata/state_cases.json, so a change on one side that is
    not mirrored on the other fails the build instead of showing the user two
    different states for one file.

    Built only with -DTDRIVE_BUILD_TESTS=ON, so installing the plugin on a
    user's machine does not pay for it. See scripts/check.sh --plugin.

    SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
    SPDX-License-Identifier: GPL-3.0-or-later
*/

#include "tdrivestate.h"

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QTextStream>

#include <fcntl.h>
#include <unistd.h>

namespace
{

QTextStream &out()
{
    static QTextStream s(stdout);
    return s;
}

/** Maps a state to the wording the fixture uses (the Go constants). */
QString stateName(TDrive::State s)
{
    switch (s) {
    case TDrive::State::Cloud:
        return QStringLiteral("cloud");
    case TDrive::State::Partial:
        return QStringLiteral("partial");
    case TDrive::State::Cached:
        return QStringLiteral("cached");
    case TDrive::State::Pinned:
        return QStringLiteral("pinned");
    case TDrive::State::Pinning:
        return QStringLiteral("pinning");
    case TDrive::State::Uploading:
        return QStringLiteral("uploading");
    case TDrive::State::Local:
        return QStringLiteral("local");
    case TDrive::State::Unknown:
        break;
    }
    return QString();
}

bool boolOr(const QJsonObject &o, const char *key, bool fallback)
{
    const QJsonValue v = o.value(QLatin1String(key));
    return v.isBool() ? v.toBool() : fallback;
}

qint64 intOr(const QJsonObject &o, const char *key, qint64 fallback)
{
    const QJsonValue v = o.value(QLatin1String(key));
    return v.isDouble() ? static_cast<qint64>(v.toDouble()) : fallback;
}

/**
 * Creates one cache entry: a sparse data file of the full size with the first
 * `filled` bytes actually written, plus rclone's metadata next to it. The Go
 * fixture builds exactly the same layout.
 */
bool writeEntry(const QString &cacheDir, const QString &remote, const QJsonObject &c)
{
    const QString rel = c.value(QLatin1String("rel")).toString();
    const QString data = cacheDir + QLatin1String("/vfs/") + remote + QLatin1Char('/') + rel;
    const QString meta = cacheDir + QLatin1String("/vfsMeta/") + remote + QLatin1Char('/') + rel;

    if (boolOr(c, "dir", false)) {
        if (!QDir().mkpath(data)) {
            return false;
        }
        const QJsonArray children = c.value(QLatin1String("children")).toArray();
        for (const QJsonValue &child : children) {
            if (!writeEntry(cacheDir, remote, child.toObject())) {
                return false;
            }
        }
        return true;
    }
    if (!boolOr(c, "present", true)) {
        return true; // nothing cached at all
    }

    const qint64 size = intOr(c, "size", 0);
    const qint64 filled = intOr(c, "filled", 0);

    QDir().mkpath(QFileInfo(data).absolutePath());
    const int fd = ::open(QFile::encodeName(data).constData(), O_CREAT | O_TRUNC | O_WRONLY, 0644);
    if (fd < 0) {
        return false;
    }
    if (filled > 0) {
        const QByteArray zeros(static_cast<int>(filled), '\0');
        if (::write(fd, zeros.constData(), zeros.size()) != zeros.size()) {
            ::close(fd);
            return false;
        }
    }
    if (::ftruncate(fd, size) != 0) {
        ::close(fd);
        return false;
    }
    ::close(fd);

    if (!boolOr(c, "meta", true)) {
        return true;
    }
    QJsonArray rs;
    const QJsonArray ranges = c.value(QLatin1String("ranges")).toArray();
    for (const QJsonValue &v : ranges) {
        const QJsonArray pair = v.toArray();
        QJsonObject r;
        r[QLatin1String("Pos")] = pair.at(0).toDouble();
        r[QLatin1String("Size")] = pair.at(1).toDouble();
        rs.append(r);
    }
    QJsonObject m;
    m[QLatin1String("Size")] = static_cast<double>(size);
    m[QLatin1String("Dirty")] = boolOr(c, "dirty", false);
    m[QLatin1String("Rs")] = rs;

    QDir().mkpath(QFileInfo(meta).absolutePath());
    QFile mf(meta);
    if (!mf.open(QIODevice::WriteOnly)) {
        return false;
    }
    mf.write(QJsonDocument(m).toJson(QJsonDocument::Compact));
    return true;
}

} // namespace

int main(int argc, char **argv)
{
    QCoreApplication app(argc, argv);
    if (argc < 3) {
        out() << "usage: tdrivestate_test <state_cases.json> <scratch dir>\n";
        return 2;
    }
    const QString fixture = QString::fromLocal8Bit(argv[1]);
    const QString scratch = QString::fromLocal8Bit(argv[2]);

    QFile f(fixture);
    if (!f.open(QIODevice::ReadOnly)) {
        out() << "cannot read " << fixture << "\n";
        return 2;
    }
    QJsonParseError perr{};
    const QJsonDocument doc = QJsonDocument::fromJson(f.readAll(), &perr);
    if (!doc.isObject()) {
        out() << "cannot parse " << fixture << ": " << perr.errorString() << "\n";
        return 2;
    }
    const QJsonArray cases = doc.object().value(QLatin1String("cases")).toArray();
    if (cases.isEmpty()) {
        out() << "the shared specification is empty\n";
        return 2;
    }

    int failed = 0;
    int n = 0;
    for (const QJsonValue &v : cases) {
        const QJsonObject c = v.toObject();
        const QString name = c.value(QLatin1String("name")).toString();
        const QString cacheDir = scratch + QLatin1String("/case") + QString::number(n++);
        if (!QDir().mkpath(cacheDir)) {
            out() << "FAIL " << name << ": cannot create " << cacheDir << "\n";
            ++failed;
            continue;
        }

        TDrive::Info info;
        info.active = true;
        info.state = QStringLiteral("idle");
        info.mode = c.value(QLatin1String("mode")).toString();
        if (info.mode.isEmpty()) {
            info.mode = QStringLiteral("stream");
        }
        info.root = QStringLiteral("/home/u/GoogleDrive");
        info.cacheDir = cacheDir;
        info.remote = QStringLiteral("gdrive");
        const QJsonArray pinned = c.value(QLatin1String("pinned")).toArray();
        for (const QJsonValue &p : pinned) {
            info.pinned << p.toString();
        }

        if (!writeEntry(cacheDir, info.remote, c)) {
            out() << "FAIL " << name << ": could not build the cache layout\n";
            ++failed;
            continue;
        }

        const QString rel = c.value(QLatin1String("rel")).toString();
        const QString want = c.value(QLatin1String("want")).toString();
        const QString got = stateName(TDrive::resolve(info, info.root + QLatin1Char('/') + rel));
        if (got != want) {
            out() << "FAIL " << name << ": state = \"" << got << "\", want \"" << want << "\"\n";
            ++failed;
        }
    }

    out() << (failed == 0 ? "ok" : "FAILED") << "  " << (cases.size() - failed) << "/" << cases.size()
          << " shared state cases\n";
    out().flush();
    return failed == 0 ? 0 : 1;
}
