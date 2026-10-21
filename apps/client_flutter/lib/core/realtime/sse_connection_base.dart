class SseMessage {
  const SseMessage({required this.event, required this.data, this.id});

  final String event;
  final String data;
  final String? id;
}

abstract interface class SseConnection {
  Stream<SseMessage> get messages;
  Future<void> close();
}
