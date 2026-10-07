# Android Node
The Android node uses app-private storage by default. External/microSD storage must be selected by the user through Android Storage Access Framework (ACTION_OPEN_DOCUMENT_TREE); the node must retain URI permissions and never bypass Android storage security.

The production agent will use a foreground service, authenticated outbound control-plane connection, reconnect backoff, battery/thermal policy and graceful node draining.
