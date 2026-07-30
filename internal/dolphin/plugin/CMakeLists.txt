# SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
# SPDX-License-Identifier: GPL-3.0-or-later

# Dolphin integration for TDrive Sync: an overlay-icon plugin that marks files
# as streamed / available offline, plus a context-menu plugin for pinning them.
#
# Built on the user's machine (see `tdrive-sync dolphin install`), because a KIO
# plugin has to match the KDE Frameworks build it is loaded into.

cmake_minimum_required(VERSION 3.20)
project(tdrive-sync-dolphin LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
if(NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE Release)
endif()

# extra-cmake-modules provides FindKF6 and the find modules KIO's own config
# file pulls in (LibMount and friends).
find_package(ECM 6.0 REQUIRED NO_MODULE)
list(APPEND CMAKE_MODULE_PATH ${ECM_MODULE_PATH})

find_package(Qt6 REQUIRED COMPONENTS Core Gui Widgets)
find_package(KF6 REQUIRED COMPONENTS KIO)

# Overlay icons (loaded by Dolphin from $QT_PLUGIN_PATH/kf6/overlayicon).
add_library(tdrivesyncoverlay MODULE overlayplugin.cpp tdrivestate.cpp)
target_link_libraries(tdrivesyncoverlay PRIVATE Qt6::Core KF6::KIOCore)

# Context-menu entries (loaded from $QT_PLUGIN_PATH/kf6/kfileitemaction).
add_library(tdrivesyncaction MODULE actionplugin.cpp tdrivestate.cpp)
target_link_libraries(tdrivesyncaction PRIVATE Qt6::Core Qt6::Gui Qt6::Widgets KF6::KIOCore KF6::KIOWidgets)

# The plugins are copied into place by `tdrive-sync dolphin install`; no install
# rules here on purpose, so nothing ever writes outside the user's home.
