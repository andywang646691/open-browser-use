#import <ApplicationServices/ApplicationServices.h>
#import <Cocoa/Cocoa.h>
#import <CoreServices/CoreServices.h>
#include <stdio.h>
#include <stdlib.h>

static NSString *helperBundlePath(void) {
    NSString *executablePath = [[NSProcessInfo processInfo] arguments].firstObject;
    NSString *marker = @"/Contents/MacOS/";
    NSRange markerRange = [executablePath rangeOfString:marker];
    if (markerRange.location != NSNotFound) {
        return [executablePath substringToIndex:markerRange.location];
    }
    return [[NSBundle mainBundle] bundlePath];
}

static void registerHelperBundle(void) {
    NSString *bundlePath = helperBundlePath();
    if (bundlePath.length == 0) return;
    LSRegisterURL((__bridge CFURLRef)[NSURL fileURLWithPath:bundlePath], true);
}

static BOOL accessibilityTrusted(BOOL requestPrompt) {
    NSApplicationLoad();
    const void *keys[] = { kAXTrustedCheckOptionPrompt };
    const void *values[] = { requestPrompt ? kCFBooleanTrue : kCFBooleanFalse };
    CFDictionaryRef options = CFDictionaryCreate(
        NULL, keys, values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    BOOL trusted = AXIsProcessTrustedWithOptions(options);
    CFRelease(options);
    return trusted;
}

static void openAccessibilitySettings(void) {
    NSString *settingsURLString = @"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility";
    NSURL *settingsURL = [NSURL URLWithString:settingsURLString];
    if (![[NSWorkspace sharedWorkspace] openURL:settingsURL]) {
        NSTask *task = [NSTask new];
        task.launchPath = @"/usr/bin/open";
        task.arguments = @[ settingsURLString ];
        [task launch];
    }
}

static void showAccessibilityGuidance(void) {
    NSString *bundlePath = helperBundlePath();
    openAccessibilitySettings();
    if (bundlePath.length > 0) {
        [[NSWorkspace sharedWorkspace] selectFile:bundlePath inFileViewerRootedAtPath:@""];
    }

    // The native TCC prompt is not guaranteed after a previous denial. Keep a
    // visible, app-owned explanation on screen so a missing prompt can never
    // look like a silent OBU failure.
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    [NSApp activateIgnoringOtherApps:YES];
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"Open Browser Use 需要辅助功能权限";
    alert.informativeText = @"请在“系统设置 → 隐私与安全性 → 辅助功能”中启用 Open Browser Use Approval Helper。系统没有自动添加时，请点击“+”，选择 Finder 中高亮的 Helper。启用后重新运行 OBU。";
    [alert addButtonWithTitle:@"打开辅助功能设置"];
    [alert addButtonWithTitle:@"在 Finder 中显示 Helper"];
    [alert addButtonWithTitle:@"关闭"];
    NSModalResponse response = [alert runModal];
    if (response == NSAlertFirstButtonReturn) {
        openAccessibilitySettings();
    } else if (response == NSAlertSecondButtonReturn && bundlePath.length > 0) {
        [[NSWorkspace sharedWorkspace] selectFile:bundlePath inFileViewerRootedAtPath:@""];
    }
}

static BOOL ensureAccessibilityPermission(void) {
    // NSApplicationLoad registers the executable as the app bundle named in
    // Info.plist before TCC evaluates it. The prompt option asks macOS to show
    // its native Accessibility prompt on first use.
    NSApplicationLoad();
    if (accessibilityTrusted(YES)) return YES;

    // TCC suppresses the native prompt after a previous denial or on managed
    // machines. Open the exact Accessibility pane and reveal this helper, then
    // return a distinct status so callers do not confuse permission failure
    // with a missing Chrome remote-debugging sheet.
    showAccessibilityGuidance();
    return NO;
}

static NSArray<NSString *> *browserBundleIDs(void) {
    return @[
        @"com.google.Chrome",
        @"com.google.Chrome.beta",
        @"com.google.Chrome.canary",
        @"com.google.Chrome.dev",
        @"org.chromium.Chromium"
    ];
}

static NSArray<NSString *> *browserNames(void) {
    return @[
        @"Google Chrome",
        @"Google Chrome Beta",
        @"Google Chrome Canary",
        @"Google Chrome Dev",
        @"Chromium"
    ];
}

static NSString *stringAttribute(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(element, attribute, &value);
    if (error != kAXErrorSuccess || value == NULL) {
        if (value != NULL) CFRelease(value);
        return @"";
    }

    NSString *result = @"";
    if (CFGetTypeID(value) == CFStringGetTypeID()) {
        result = [(__bridge NSString *)value copy];
    }
    CFRelease(value);
    return result;
}

static CFArrayRef copyArrayAttribute(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(element, attribute, &value);
    if (error != kAXErrorSuccess || value == NULL) {
        if (value != NULL) CFRelease(value);
        return NULL;
    }
    if (CFGetTypeID(value) != CFArrayGetTypeID()) {
        CFRelease(value);
        return NULL;
    }
    return (CFArrayRef)value;
}

static BOOL hasExactValue(AXUIElementRef element, NSArray<NSString *> *values) {
    for (NSString *value in @[
        stringAttribute(element, kAXTitleAttribute),
        stringAttribute(element, kAXDescriptionAttribute),
        stringAttribute(element, kAXValueAttribute)
    ]) {
        if ([values containsObject:value]) return YES;
    }
    return NO;
}

static BOOL isButton(AXUIElementRef element) {
    return [stringAttribute(element, kAXRoleAttribute) isEqualToString:(__bridge NSString *)kAXButtonRole];
}

static BOOL isAllowedButtonLabel(AXUIElementRef element, NSArray<NSString *> *labels) {
    return isButton(element) && hasExactValue(element, labels);
}

static BOOL pressButtonInTree(AXUIElementRef root, NSArray<NSString *> *labels, NSUInteger depth) {
    if (root == NULL || depth > 16) return NO;

    if (isAllowedButtonLabel(root, labels)) {
        AXError pressError = AXUIElementPerformAction(root, kAXPressAction);
        if (pressError == kAXErrorSuccess) return YES;
    }

    CFArrayRef children = copyArrayAttribute(root, kAXChildrenAttribute);
    if (children == NULL) return NO;

    BOOL pressed = NO;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex index = 0; index < count && !pressed; index++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, index);
        pressed = pressButtonInTree(child, labels, depth + 1);
    }
    CFRelease(children);
    return pressed;
}

static BOOL isApprovalSheet(AXUIElementRef sheet) {
    NSArray<NSString *> *labels = @[
        @"Allow remote debugging?",
        @"要允许远程调试吗？",
        @"要允许远程调试吗?"
    ];
    NSString *title = stringAttribute(sheet, kAXTitleAttribute);
    NSString *description = stringAttribute(sheet, kAXDescriptionAttribute);
    NSString *value = stringAttribute(sheet, kAXValueAttribute);
    NSString *lowerTitle = title.lowercaseString;
    NSString *lowerDescription = description.lowercaseString;
    NSString *lowerValue = value.lowercaseString;
    return [labels containsObject:title] || [labels containsObject:description] || [labels containsObject:value] ||
           [lowerTitle containsString:@"remote debugging"] ||
           [lowerDescription containsString:@"remote debugging"] ||
           [lowerValue containsString:@"remote debugging"] ||
           [title containsString:@"远程调试"] ||
           [description containsString:@"远程调试"] ||
           [value containsString:@"远程调试"];
}

static BOOL containsApprovalMarker(AXUIElementRef element, NSUInteger depth) {
    if (element == NULL || depth > 24) return NO;
    if (isApprovalSheet(element)) return YES;

    CFArrayRef children = copyArrayAttribute(element, kAXChildrenAttribute);
    if (children == NULL) return NO;

    BOOL found = NO;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex index = 0; index < count && !found; index++) {
        found = containsApprovalMarker((AXUIElementRef)CFArrayGetValueAtIndex(children, index), depth + 1);
    }
    CFRelease(children);
    return found;
}

static BOOL isApprovalContainer(AXUIElementRef element) {
    NSString *role = stringAttribute(element, kAXRoleAttribute);
    NSString *subrole = stringAttribute(element, kAXSubroleAttribute);
    return [role isEqualToString:(__bridge NSString *)kAXSheetRole] ||
           [subrole isEqualToString:@"AXApplicationAlertDialog"];
}

static BOOL findAndApproveInTree(AXUIElementRef element, NSUInteger depth) {
    if (element == NULL || depth > 20) return NO;
    if (isApprovalContainer(element) && containsApprovalMarker(element, 0)) {
        return pressButtonInTree(element, @[@"Allow", @"允许"], 0);
    }
    CFArrayRef children = copyArrayAttribute(element, kAXChildrenAttribute);
    if (children == NULL) return NO;
    BOOL approved = NO;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex index = 0; index < count && !approved; index++) {
        approved = findAndApproveInTree((AXUIElementRef)CFArrayGetValueAtIndex(children, index), depth + 1);
    }
    CFRelease(children);
    return approved;
}

static BOOL approveInApplication(AXUIElementRef application) {
    CFArrayRef windows = copyArrayAttribute(application, kAXWindowsAttribute);
    if (windows == NULL) return NO;

    BOOL approved = NO;
    CFIndex windowCount = CFArrayGetCount(windows);
    for (CFIndex windowIndex = 0; windowIndex < windowCount && !approved; windowIndex++) {
        AXUIElementRef window = (AXUIElementRef)CFArrayGetValueAtIndex(windows, windowIndex);
        CFArrayRef sheets = copyArrayAttribute(window, CFSTR("AXSheets"));
        if (sheets != NULL) {
            CFIndex sheetCount = CFArrayGetCount(sheets);
            for (CFIndex sheetIndex = 0; sheetIndex < sheetCount && !approved; sheetIndex++) {
                AXUIElementRef sheet = (AXUIElementRef)CFArrayGetValueAtIndex(sheets, sheetIndex);
                if (containsApprovalMarker(sheet, 0)) {
                    approved = pressButtonInTree(sheet, @[@"Allow", @"允许"], 0);
                }
            }
            CFRelease(sheets);
        }
        if (!approved) approved = findAndApproveInTree(window, 0);
    }
    CFRelease(windows);
    return approved;
}

static BOOL isBanner(AXUIElementRef element) {
    NSArray<NSString *> *containers = @[ @"Info bar container", @"信息栏容器" ];
    NSArray<NSString *> *markers = @[
        @"自动测试软件正在控制 Chrome",
        @"Chrome is being controlled by automated test software"
    ];
    return [containers containsObject:stringAttribute(element, kAXTitleAttribute)] ||
           [containers containsObject:stringAttribute(element, kAXDescriptionAttribute)] ||
           [markers containsObject:stringAttribute(element, kAXTitleAttribute)] ||
           [markers containsObject:stringAttribute(element, kAXDescriptionAttribute)] ||
           [markers containsObject:stringAttribute(element, kAXValueAttribute)];
}

static BOOL dismissInTree(AXUIElementRef element, NSUInteger depth) {
    if (element == NULL || depth > 16) return NO;
    if (isBanner(element)) {
        return pressButtonInTree(element, @[@"Close", @"Close button", @"关闭", @"关闭按钮"], 0);
    }

    CFArrayRef children = copyArrayAttribute(element, kAXChildrenAttribute);
    if (children == NULL) return NO;

    BOOL dismissed = NO;
    CFIndex count = CFArrayGetCount(children);
    for (CFIndex index = 0; index < count && !dismissed; index++) {
        AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, index);
        dismissed = dismissInTree(child, depth + 1);
    }
    CFRelease(children);
    return dismissed;
}

static BOOL dismissInApplication(AXUIElementRef application) {
    CFArrayRef windows = copyArrayAttribute(application, kAXWindowsAttribute);
    if (windows == NULL) return NO;

    BOOL dismissed = NO;
    CFIndex count = CFArrayGetCount(windows);
    for (CFIndex index = 0; index < count && !dismissed; index++) {
        AXUIElementRef window = (AXUIElementRef)CFArrayGetValueAtIndex(windows, index);
        dismissed = dismissInTree(window, 0);
    }
    CFRelease(windows);
    return dismissed;
}

static BOOL isBrowser(NSRunningApplication *application) {
    NSString *bundleID = application.bundleIdentifier ?: @"";
    NSString *name = application.localizedName ?: @"";
    return [browserBundleIDs() containsObject:bundleID] || [browserNames() containsObject:name];
}

static BOOL runAppleScriptResource(NSString *resourceName, NSString **result) {
    NSString *path = [[NSBundle mainBundle] pathForResource:resourceName ofType:@"applescript"];
    if (path.length == 0) return NO;

    NSDictionary *errorInfo = nil;
    NSAppleScript *script = [[NSAppleScript alloc] initWithContentsOfURL:[NSURL fileURLWithPath:path] error:&errorInfo];
    if (script == nil) return NO;
    NSAppleEventDescriptor *value = [script executeAndReturnError:&errorInfo];
    if (value == nil) return NO;
    if (result != NULL) *result = value.stringValue ?: @"";
    return YES;
}

static BOOL performOperation(NSString *operation) {
    BOOL changed = NO;
    for (NSRunningApplication *application in [NSWorkspace sharedWorkspace].runningApplications) {
        if (!isBrowser(application)) continue;
        AXUIElementRef axApplication = AXUIElementCreateApplication(application.processIdentifier);
        if (axApplication == NULL) continue;
        if ([operation isEqualToString:@"approve"]) {
            changed = approveInApplication(axApplication) || changed;
        } else if ([operation isEqualToString:@"dismiss-banner"]) {
            changed = dismissInApplication(axApplication) || changed;
        }
        CFRelease(axApplication);
    }
    return changed;
}

static BOOL performDismissOnce(void) {
    NSString *result = nil;
    if (runAppleScriptResource(@"open-browser-use-dismiss-banner", &result)) {
        if ([result isEqualToString:@"dismissed"]) return YES;
        if (![result isEqualToString:@"not-found"]) return NO;
    }
    return performOperation(@"dismiss-banner");
}

static BOOL performApprovalOnce(void) {
    NSString *result = nil;
    if (runAppleScriptResource(@"open-browser-use-approve", &result)) {
        if ([result isEqualToString:@"ready"]) return YES;
        if (![result isEqualToString:@"not-found"]) return NO;
    }
    return performOperation(@"approve");
}

static NSTimeInterval approvalWaitSeconds(int argc, const char *argv[]) {
    if (argc < 3) return 30.0;
    char *end = NULL;
    double seconds = strtod(argv[2], &end);
    if (end == argv[2] || *end != '\0' || seconds <= 0.0 || seconds > 300.0) {
        return 30.0;
    }
    return seconds;
}

static BOOL waitForApproval(int argc, const char *argv[]) {
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:approvalWaitSeconds(argc, argv)];
    while ([deadline timeIntervalSinceNow] > 0.0) {
        if (performApprovalOnce()) return YES;
        [NSThread sleepForTimeInterval:0.12];
    }
    return NO;
}

static BOOL dismissWaitSeconds(int argc, const char *argv[]) {
    NSTimeInterval seconds = 15.0;
    if (argc >= 3) {
        char *end = NULL;
        double parsed = strtod(argv[2], &end);
        if (end != argv[2] && *end == '\0' && parsed > 0.0 && parsed <= 120.0) {
            seconds = parsed;
        }
    }

    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:seconds];
    while ([deadline timeIntervalSinceNow] > 0.0) {
        if (performDismissOnce()) return YES;
        [NSThread sleepForTimeInterval:0.12];
    }
    return NO;
}

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        registerHelperBundle();
        if (argc < 2) return 2;
        NSString *operation = [NSString stringWithUTF8String:argv[1]];
        if (![operation isEqualToString:@"approve"] &&
            ![operation isEqualToString:@"approve-wait"] &&
            ![operation isEqualToString:@"dismiss-banner-wait"] &&
            ![operation isEqualToString:@"dismiss-banner"]) return 2;
        if (!ensureAccessibilityPermission()) return 3;

        if ([operation isEqualToString:@"approve-wait"]) {
            return waitForApproval(argc, argv) ? 0 : 1;
        }

        if ([operation isEqualToString:@"dismiss-banner-wait"]) {
            return dismissWaitSeconds(argc, argv) ? 0 : 1;
        }

        NSString *result = nil;
        NSString *resource = [operation isEqualToString:@"approve"]
            ? @"open-browser-use-approve"
            : @"open-browser-use-dismiss-banner";
        if (runAppleScriptResource(resource, &result)) {
            if ([result isEqualToString:@"ready"] || [result isEqualToString:@"dismissed"]) return 0;
            if ([result isEqualToString:@"not-found"]) return 1;
        }
        return performOperation(operation) ? 0 : 1;
    }
}
