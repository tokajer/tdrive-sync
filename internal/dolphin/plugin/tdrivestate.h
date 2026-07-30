/*
    Shared state resolution for the TDrive Sync Dolphin integration.

    SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
    SPDX-License-Identifier: GPL-3.0-or-later
*/

#pragma once

#include <QString>
#include <QStringList>

namespace TDrive
{

/** Per-file sync state, mirroring the Go package fmstate. */
enum class State {
    Unknown, /**< not inside the sync folder – no overlay */
    Cloud, /**< nothing cached: opening it downloads it */
    Partial, /**< partially cached (folders: something inside is cached) */
    Cached, /**< fully cached, usable offline, not explicitly pinned */
    Pinned, /**< marked "keep offline", local copy complete */
    Pinning, /**< marked "keep offline", download not finished */
    Uploading, /**< local changes not yet written back to Drive */
    Local, /**< mirror mode: a real local copy anyway */
};

/**
 * The snapshot the daemon publishes. Everything the plugin needs to answer
 * without talking to the daemon.
 */
struct Info {
    bool active = false;
    QString mode; /**< "stream" or "mirror" */
    QString state; /**< coarse daemon state, e.g. "idle" */
    QString root; /**< mount point / mirror root, no trailing slash */
    QString cacheDir; /**< rclone --cache-dir, holds vfs/ and vfsMeta/ */
    QString remote; /**< rclone remote name */
    QString exec; /**< the daemon binary, for CLI calls */
    QStringList pinned; /**< Drive-relative paths marked "keep offline" */

    /** Whether indicators should be shown at all. */
    bool usable() const
    {
        return active && !root.isEmpty() && !cacheDir.isEmpty() && !remote.isEmpty();
    }
};

/** Path of the file published by the daemon (see the Go package fmstate). */
QString statePath();

/** Reads that file; returns an unusable Info on any problem. */
Info loadInfo();

/**
 * Drive-relative form of an absolute local path, or an empty string when the
 * path is outside the sync folder (the sync folder itself included).
 */
QString relativePath(const Info &info, const QString &localFile);

/** Whether a Drive-relative path is pinned, directly or via a parent folder. */
bool isPinned(const Info &info, const QString &rel);

/**
 * State of an absolute local path.
 *
 * Resolved entirely from rclone's on-disk cache, never by touching the mount:
 * a stat on an unresponsive FUSE mount would block the file manager's UI, and
 * KOverlayIconPlugin::getOverlays() must not block.
 */
State resolve(const Info &info, const QString &localFile);

/** Icon name to overlay for a state, empty for State::Unknown. */
QString overlayIcon(State state);

/**
 * Picks the German or the English wording, following the same rule as the rest
 * of the app: German for a German locale, English for everything else.
 */
QString text(const char *english, const char *german);

/** Human-readable, localised description of a state. */
QString stateLabel(State state);

}
