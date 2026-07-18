# CMake 3.21 is required by Qt's Protobuf/gRPC target helpers.
cmake_minimum_required(VERSION 3.21)

if(NOT DEFINED WHATEVR_VERSION)
    if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION")
        file(READ "${CMAKE_CURRENT_SOURCE_DIR}/../VERSION" WHATEVR_VERSION)
        string(STRIP "${WHATEVR_VERSION}" WHATEVR_VERSION)
        string(REGEX MATCH "^[0-9]+\.[0-9]+\.[0-9]+" WHATEVR_VERSION "${WHATEVR_VERSION}")
    endif()
    if(NOT WHATEVR_VERSION)
        set(WHATEVR_VERSION "0.0.0")
    endif()
endif()

if(NOT DEFINED WHATEVR_VERSION_FULL)
    set(WHATEVR_VERSION_FULL "${WHATEVR_VERSION}")
endif()

project(whatkevr VERSION ${WHATEVR_VERSION} LANGUAGES CXX)

set(QT_MIN_VERSION "6.10")
set(KF_MIN_VERSION "6.27")
set(KIRIGAMI_ADDONS_MIN_VERSION "1.12.0")

find_package(ECM ${KF_MIN_VERSION} REQUIRED NO_MODULE)
set(CMAKE_MODULE_PATH ${ECM_MODULE_PATH})

include(FeatureSummary)
include(ECMFindQmlModule)
include(KDECompilerSettings NO_POLICY_SCOPE)
include(KDEInstallDirs)
include(KDECMakeSettings)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(KDE_COMPILERSETTINGS_LEVEL ${KF_MIN_VERSION})

find_package(Qt6 ${QT_MIN_VERSION} REQUIRED COMPONENTS
    Core
    Gui
    Grpc
    Network
    Protobuf
    Qml
    Quick
    QuickControls2
    ShaderTools
    Widgets
)

find_package(KF6 ${KF_MIN_VERSION} REQUIRED COMPONENTS
    ColorScheme
    CoreAddons
    DBusAddons
    I18n
    Kirigami
    Prison
    QQC2DesktopStyle
)

find_package(KF6KirigamiAddons ${KIRIGAMI_ADDONS_MIN_VERSION} REQUIRED)
find_package(PkgConfig REQUIRED)
pkg_check_modules(RLottie REQUIRED IMPORTED_TARGET rlottie)

ecm_find_qmlmodule(org.kde.ki18n REQUIRED)
ecm_find_qmlmodule(org.kde.desktop REQUIRED)
ecm_find_qmlmodule(org.kde.kirigami REQUIRED)
ecm_find_qmlmodule(org.kde.kirigamiaddons.components REQUIRED)
ecm_find_qmlmodule(org.kde.kirigamiaddons.delegates REQUIRED)
ecm_find_qmlmodule(org.kde.kirigamiaddons.formcard REQUIRED)
ecm_find_qmlmodule(org.kde.kirigamiaddons.settings REQUIRED)

if(QT_KNOWN_POLICY_QTP0004)
    qt_policy(SET QTP0004 NEW)
endif()

qt_standard_project_setup(REQUIRES ${QT_MIN_VERSION})

# Off by default so `just build` never needs Qt6::Test; enable with
# -DWHATEVR_BUILD_TESTS=ON to build and run the protocol-core unit tests.
option(WHATEVR_BUILD_TESTS "Build whatkevr unit tests" OFF)

add_subdirectory(src)

if(WHATEVR_BUILD_TESTS)
    find_package(Qt6 ${QT_MIN_VERSION} REQUIRED COMPONENTS Test)
    enable_testing()
    add_subdirectory(tests)
endif()

install(FILES data/in.codelif.Whatevr.desktop DESTINATION ${KDE_INSTALL_APPDIR})
install(FILES data/in.codelif.Whatevr.metainfo.xml DESTINATION ${KDE_INSTALL_METAINFODIR})
install(FILES data/in.codelif.Whatevr.xml DESTINATION ${KDE_INSTALL_MIMEDIR})
install(FILES data/icons/in.codelif.Whatevr.svg DESTINATION ${KDE_INSTALL_ICONDIR}/hicolor/scalable/apps)

feature_summary(WHAT ALL FATAL_ON_MISSING_REQUIRED_PACKAGES)
