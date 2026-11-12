import 'package:duanju_app/playback_recovery.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('PlaybackRecovery 6 级协调器', () {
    test('从 buffering_timeout 起步，逐级单调升级且不无限循环', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now(), hasAlternative: true);

      // 第 1 次：buffering_timeout -> 第 3 级 replay
      var decision = recovery.start(reason: 'buffering_timeout', context: context)!;
      expect(decision.level, 3);
      expect(decision.action, PlaybackRecoveryAction.replay);
      recovery.complete(decision, recovered: false);

      // 第 2 次：replay 失败 -> 第 4 级 reopen
      decision = recovery.start(reason: 'replay_failed', context: context)!;
      expect(decision.level, 4);
      expect(decision.action, PlaybackRecoveryAction.reopen);
      recovery.complete(decision, recovered: false);

      // 第 3 次：reopen 失败 -> 第 5 级 alternative（有备选线路）
      decision = recovery.start(reason: 'reopen_failed', context: context)!;
      expect(decision.level, 5);
      expect(decision.action, PlaybackRecoveryAction.alternative);
      recovery.complete(decision, recovered: false);

      // 第 4 次：alternative 失败 -> 第 6 级 rebuild
      decision = recovery.start(reason: 'alternative_failed', context: context)!;
      expect(decision.level, 6);
      expect(decision.action, PlaybackRecoveryAction.rebuild);
      recovery.complete(decision, recovered: false);

      // 第 5 次：超过 6 级 -> stop，绝不无限循环
      decision = recovery.start(reason: 'rebuild_failed', context: context)!;
      expect(decision.exhausted, isTrue);
      expect(decision.action, PlaybackRecoveryAction.stop);
    });

    test('streamEpochStale 直接跳到刷新级（第 5 级），不浪费低级动作', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(
        now: DateTime.now(),
        streamEpochStale: true,
        hasAlternative: true,
      );
      // 即使起因是第 2/3 级，监听重建后旧 session 已失效，必须直接走刷新。
      final decision = recovery.start(reason: 'surface_cover_timeout', context: context)!;
      expect(decision.level, 5);
      expect(decision.action, PlaybackRecoveryAction.refresh);
    });

    test('网络不可达立即 stop，不浪费恢复次数', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now(), networkReachable: false);
      final decision = recovery.start(reason: 'buffering_timeout', context: context)!;
      expect(decision.exhausted, isTrue);
      expect(decision.action, PlaybackRecoveryAction.stop);
      // 网络拦截不应计入失败尝试，否则会提前耗尽次数。
      expect(recovery.attempts, 0);
    });

    test('本地文件直接 stop，不尝试换线路/重建', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now(), local: true);
      final decision = recovery.start(reason: 'buffering_timeout', context: context)!;
      expect(decision.action, PlaybackRecoveryAction.stop);
      expect(decision.message, contains('本地'));
    });

    test('恢复成功 reset 清零计数，避免后续偶发故障被旧计数判死', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now(), hasAlternative: true);
      final decision = recovery.start(reason: 'buffering_timeout', context: context)!;
      recovery.complete(decision, recovered: true);
      expect(recovery.lastLevel, 0);
      expect(recovery.attempts, 0);
      expect(recovery.exhausted, isFalse);
    });

    test('退避随次数增加但封顶，不超过 maxBackoff', () {
      final recovery = PlaybackRecovery(
        baseBackoff: const Duration(seconds: 1),
        maxBackoff: const Duration(seconds: 8),
      );
      final context = PlaybackRecoveryContext(now: DateTime.now(), hasAlternative: true);
      // 用从 3 级起步的 reason 序列，让协调器在耗尽前走完 3→4→5→6 各级，
      // 从而收集到随次数递增、并在第 4 级封顶的退避值。
      final reasons = [
        'buffering_timeout',
        'replay_failed',
        'reopen_failed',
        'alternative_failed',
      ];
      final backoffs = <Duration>[];
      for (final reason in reasons) {
        final decision = recovery.start(reason: reason, context: context)!;
        backoffs.add(decision.backoff);
        recovery.complete(decision, recovered: false);
      }
      // 单调不降
      for (var i = 1; i < backoffs.length; i++) {
        expect(backoffs[i] >= backoffs[i - 1], isTrue,
            reason: 'backoff 应在第 $i 步保持不降');
      }
      // 封顶 8s（第 4 级：1s<<3 = 8s）
      expect(backoffs.last, const Duration(seconds: 8));
    });

    test('正在进行的恢复任务通过 token 互斥，不并发', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now());
      final first = recovery.start(reason: 'buffering_timeout', context: context)!;
      // 不调用 complete，模拟任务进行中
      final second = recovery.start(reason: 'buffering_timeout', context: context);
      expect(second, isNull);
      recovery.complete(first, recovered: false);
    });

    test('窗口内持续失败耗尽后 stop，reset 可重新给机会', () {
      final recovery = PlaybackRecovery();
      final context = PlaybackRecoveryContext(now: DateTime.now(), hasAlternative: true);
      for (var i = 0; i < 6; i++) {
        final decision = recovery.start(reason: 'failed_$i', context: context)!;
        recovery.complete(decision, recovered: false);
        if (decision.exhausted) break;
      }
      expect(recovery.exhausted, isTrue);
      recovery.reset();
      expect(recovery.exhausted, isFalse);
      expect(recovery.attempts, 0);
      // reset 后又能从低级别开始
      final decision = recovery.start(reason: 'buffering_timeout', context: context)!;
      expect(decision.level, 3);
    });
  });
}
