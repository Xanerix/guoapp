import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../native_text_input.dart';
import '../app_scope.dart';

class FormTextField extends StatelessWidget {
  const FormTextField({
    super.key,
    required this.controller,
    required this.decoration,
    this.focusNode,
    this.obscure = false,
    this.url = false,
    this.enabled = true,
    this.autofocus = false,
    this.maxLength,
    this.onSubmitted,
  });
  final TextEditingController controller;
  final InputDecoration decoration;
  final FocusNode? focusNode;
  final bool obscure;
  final bool url;
  final bool enabled;
  final bool autofocus;
  final int? maxLength;
  final ValueChanged<String>? onSubmitted;

  @override
  Widget build(BuildContext context) =>
      AppScope.of(context).television &&
          defaultTargetPlatform == TargetPlatform.android
      ? _TelevisionTextField(field: this)
      : TextField(
          controller: controller,
          focusNode: focusNode,
          enabled: enabled,
          autofocus: autofocus,
          obscureText: obscure,
          keyboardType: url ? TextInputType.url : null,
          enableSuggestions: !obscure && !url,
          autocorrect: !obscure && !url,
          inputFormatters: [
            if (maxLength != null) LengthLimitingTextInputFormatter(maxLength),
          ],
          onSubmitted: onSubmitted,
          decoration: decoration,
        );
}

class _TelevisionTextField extends StatefulWidget {
  const _TelevisionTextField({required this.field});
  final FormTextField field;

  @override
  State<_TelevisionTextField> createState() => _TelevisionTextFieldState();
}

class _TelevisionTextFieldState extends State<_TelevisionTextField> {
  final _focus = FocusNode();
  var _editing = false;

  FormTextField get _field => widget.field;
  FocusNode get _focusNode => _field.focusNode ?? _focus;

  @override
  void didUpdateWidget(_TelevisionTextField oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_editing && !_field.enabled) NativeTextInput.stop();
  }

  @override
  void dispose() {
    if (_editing) NativeTextInput.stop();
    _focus.dispose();
    super.dispose();
  }

  void _edit() {
    setState(() => _editing = true);
    NativeTextInput.start(
      _field.controller.text,
      search: false,
      password: _field.obscure,
      url: _field.url,
      maxLength: _field.maxLength,
      onChanged: (text) {
        if (mounted) _field.controller.text = text;
      },
      onDone: (text, submit) {
        if (!mounted) return;
        setState(() => _editing = false);
        _field.controller.text = text;
        if (submit) _field.onSubmitted?.call(text);
      },
    );
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([_field.controller, _focusNode]),
    builder: (context, _) {
      final text = _field.controller.text;
      return InkWell(
        focusNode: _focusNode,
        autofocus: _field.autofocus,
        canRequestFocus: _field.enabled,
        focusColor: Colors.transparent,
        borderRadius: BorderRadius.circular(12),
        onTap: _field.enabled ? _edit : null,
        child: InputDecorator(
          decoration: _field.decoration.copyWith(enabled: _field.enabled),
          isFocused: _focusNode.hasFocus,
          isEmpty: text.isEmpty,
          child: Text.rich(
            TextSpan(
              text: _field.obscure ? '•' * text.length : text,
              children: [
                if (_editing)
                  WidgetSpan(
                    alignment: PlaceholderAlignment.middle,
                    child: Container(
                      width: 2,
                      height: 18,
                      margin: const EdgeInsets.only(left: 1),
                      color: Theme.of(context).colorScheme.primary,
                    ),
                  ),
              ],
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.bodyLarge,
          ),
        ),
      );
    },
  );
}
