# Protocol-core unit tests. The tested sources depend only on Qt6::Core and
# Qt6::Network, so the test compiles them directly rather than dragging in the
# whole GUI target.
qt_add_executable(tst_protocolcore
    tst_protocolcore.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/protocolclient.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/collectionviewmodel.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol/objectviewmodel.cpp
)

target_include_directories(tst_protocolcore PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../src/protocol
)

target_link_libraries(tst_protocolcore PRIVATE
    Qt6::Core
    Qt6::Network
    Qt6::Test
)

add_test(NAME tst_protocolcore COMMAND tst_protocolcore)
