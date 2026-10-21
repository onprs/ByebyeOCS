import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/models/question.dart';

final questionOfferingsProvider =
    FutureProvider.autoDispose<List<QuestionOffering>>((ref) async {
  ref.watch(sessionEpochProvider);
  return ref.watch(questionRepositoryProvider).fetchOfferings();
});

final questionQueryProvider =
    FutureProvider.autoDispose.family<QuestionQuery, String>((ref, id) async {
  ref.watch(sessionEpochProvider);
  return ref.watch(questionRepositoryProvider).fetchQuery(id);
});
