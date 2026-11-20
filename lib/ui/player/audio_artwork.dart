import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../models.dart';
import '../widgets/cover_backdrop.dart';
import '../widgets/cover_image.dart';

class AudioArtwork extends StatelessWidget {
  const AudioArtwork({super.key, required this.drama});
  final Drama drama;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: Colors.black,
    child: Stack(
      fit: StackFit.expand,
      children: [
        CoverBackdrop(
          drama: drama,
          opacity: .6,
          overlay: const LinearGradient(
            colors: [Color(0x66000000), Color(0x66000000)],
          ),
        ),
        LayoutBuilder(
          builder: (context, box) {
            final height = math.min(
              box.maxHeight * .5,
              box.maxWidth * .6 * 4 / 3,
            );
            return Center(
              child: SizedBox(
                width: height * 3 / 4,
                height: height,
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    borderRadius: BorderRadius.circular(16),
                    boxShadow: const [
                      BoxShadow(color: Color(0x66000000), blurRadius: 24),
                    ],
                  ),
                  child: DramaCover(drama: drama, radius: 16, details: false),
                ),
              ),
            );
          },
        ),
      ],
    ),
  );
}
