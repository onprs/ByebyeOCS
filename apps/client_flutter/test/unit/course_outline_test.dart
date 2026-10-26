import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/core/models/plan.dart';

import '../fixtures/course_outline.dart';

void main() {
  test('目录保留平台顺序、编号与父子关系，子树计数排除普通资料', () {
    final detail = CourseDetail.parse(courseOutlineFixture);
    final tree = detail.chapterTree;
    expect(tree.length, 2);
    final intro = tree.first;
    expect(intro.chapter.displayTitle, '1 课程导学');
    expect(
        intro.children.map((node) => node.chapter.externalId), ['900', '50']);
    expect(intro.children.first.children.single.chapter.displayTitle,
        '1.1.1 学习目标');
    expect(intro.children.first.children.single.children.single.depth, 3);
    expect(intro.taskPointCount, 1);
    expect(intro.confirmedCount, 1);
    expect(tree.last.taskPointCount, 1);
    expect(tree.last.confirmedCount, 0);
    expect(intro.selectableChapters.map((chapter) => chapter.id),
        ['chapter-900', 'chapter-100', 'chapter-700', 'chapter-50']);
  });

  test('旧平铺数据、缺失父节点和环不会丢失或重复章节', () {
    const chapters = [
      CourseChapter(id: 'legacy', title: '旧章节'),
      CourseChapter(
          id: 'orphan',
          externalId: '1',
          parentExternalId: 'missing',
          title: '章节'),
      CourseChapter(
          id: 'cycle-a', externalId: '2', parentExternalId: '3', title: '章节'),
      CourseChapter(
          id: 'cycle-b', externalId: '3', parentExternalId: '2', title: '章节'),
    ];
    final nodes = CourseChapterNode.build(chapters)
        .expand((root) => root.subtree)
        .toList();
    expect(nodes.length, chapters.length);
    expect(
        nodes.map((node) => node.chapter.id).toSet().length, chapters.length);
  });

  test('批量勾选的章节范围遵循服务端 200 章上限', () {
    final draft = PlanDraft(
      name: '课程安排',
      courseIds: const ['c1'],
      scopeKind: 'selected',
      chapters: List.generate(
          201,
          (index) =>
              PlanChapterRef(courseId: 'c1', chapterId: 'chapter-$index')),
    );
    expect(draft.validate().any((message) => message.contains('200')), isTrue);
    expect(
        draft.copyWith(chapters: draft.chapters.take(200).toList()).validate(),
        isEmpty);
  });
}
