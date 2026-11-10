const appFlavor = String.fromEnvironment(
  'APP_FLAVOR',
  defaultValue: 'hongguojian',
);
const appName = appFlavor == 'hongguojian'
    ? '红果鉴'
    : String.fromEnvironment('ZHENGUOJIAN_DISPLAY_NAME', defaultValue: '真果鉴');
const appSlug = appFlavor;
