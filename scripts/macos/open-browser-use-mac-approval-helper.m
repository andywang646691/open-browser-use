// Open Browser Use macOS approval helper.
//
// The only OBU process that uses macOS Accessibility. It approves Chrome's
// "Allow remote debugging?" sheet for Direct CDP and closes the resulting
// "controlled by automated test software" infobar.
//
// Operations:
//   approve-then-dismiss-wait [approveSec] [dismissSec]  launcher default
//   approve-wait [sec] | dismiss-banner-wait [sec]
//   approve | dismiss-banner                               one-shot probes
//   status   report Accessibility trust without prompting
//   setup    interactive Accessibility guidance (never run in the background)
//   dump     print browser-native AX tree (web content skipped) for diagnosis
//   probe    report what approve/dismiss would press, without pressing
//   self-test  pure matcher checks, needs no permission
//
// Exit codes: 0 done, 1 not found before deadline, 2 usage, 3 Accessibility
// permission missing, 4 another helper already owns this phase, 5 caller
// exited while no approval sheet was visible.
//
// Every phase writes JSONL diagnostics to OBU_LOG_FILE (default
// ~/Library/Logs/OpenBrowserUse/native-host.log) because a one-shot CLI
// launcher may exit before the helper finishes.

#import <ApplicationServices/ApplicationServices.h>
#import <Cocoa/Cocoa.h>
#import <CoreServices/CoreServices.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <mach-o/dyld.h>
#include <signal.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

enum {
    kExitDone = 0,
    kExitNotFound = 1,
    kExitUsage = 2,
    kExitNoPermission = 3,
    kExitBusy = 4,
    kExitCallerGone = 5,
};

static const useconds_t kPollInterval = 150 * 1000;
static const NSUInteger kMaxSheetDepth = 12;
static const NSUInteger kMaxWindowDepth = 18;
static NSString *const kDisclaimedEnv = @"OBU_HELPER_DISCLAIMED";

#pragma mark - Pure matchers (covered by self-test)

static BOOL isApprovalText(NSString *text) {
    if (text.length == 0) return NO;
    if ([@[ @"Allow remote debugging?", @"要允许远程调试吗？", @"要允许远程调试吗?" ] containsObject:text]) return YES;
    return [text.lowercaseString containsString:@"remote debugging"] || [text containsString:@"远程调试"];
}

static BOOL isAllowLabel(NSString *text) {
    return [@[ @"Allow", @"允许" ] containsObject:text];
}

static BOOL isBannerMarkerText(NSString *text) {
    if (text.length == 0) return NO;
    return [text.lowercaseString containsString:@"being controlled by automated test software"] ||
           [text containsString:@"自动测试软件"];
}

static BOOL isCloseLabel(NSString *text) {
    // Exact labels only: the same infobar also exposes a "turn off in
    // settings" control that must never be pressed.
    return [@[ @"Close", @"Close button", @"Dismiss", @"关闭", @"关闭按钮" ] containsObject:text];
}

static BOOL isInfoBarContainerLabel(NSString *text) {
    return [@[ @"Info bar container", @"信息栏容器" ] containsObject:text];
}

// Chrome hosts installed web-app (PWA) windows in per-app shim processes
// ("<browser bundle id>.app.<app id>"). Chrome attaches the remote-debugging
// sheet to the most recently active browser window, which is often a PWA
// window, so those shims must be scanned like the browser itself.
static BOOL isBrowserBundleID(NSString *bundleID) {
    if (bundleID.length == 0) return NO;
    NSArray *browsers = @[ @"com.google.Chrome", @"com.google.Chrome.beta", @"com.google.Chrome.canary",
                           @"com.google.Chrome.dev", @"org.chromium.Chromium" ];
    for (NSString *browser in browsers) {
        if ([bundleID isEqualToString:browser]) return YES;
        NSString *appPrefix = [browser stringByAppendingString:@".app."];
        if ([bundleID hasPrefix:appPrefix] && bundleID.length > appPrefix.length) return YES;
    }
    return NO;
}

static NSTimeInterval parseSeconds(const char *raw, NSTimeInterval fallback, NSTimeInterval max) {
    if (raw == NULL) return fallback;
    char *end = NULL;
    double value = strtod(raw, &end);
    if (end == raw || *end != '\0' || value <= 0.0 || value > max) return fallback;
    return value;
}

static pid_t callerPID(void) {
    const char *raw = getenv("OBU_MAC_APPROVAL_PARENT_PID");
    if (raw == NULL || *raw == '\0') return 0;
    char *end = NULL;
    long value = strtol(raw, &end, 10);
    if (end == raw || *end != '\0' || value <= 1 || value > INT_MAX) return 0;
    return (pid_t)value;
}

static BOOL callerAlive(pid_t pid) {
    if (pid <= 1) return YES;  // unknown caller: never stop early
    if (kill(pid, 0) == 0) return YES;
    return errno == EPERM;
}

#pragma mark - Process identity

static NSString *executablePath(void) {
    char path[PATH_MAX];
    uint32_t size = sizeof(path);
    if (_NSGetExecutablePath(path, &size) != 0) return @"";
    char resolved[PATH_MAX];
    if (realpath(path, resolved) == NULL) return [NSString stringWithUTF8String:path];
    return [NSString stringWithUTF8String:resolved];
}

static NSString *helperBundlePath(void) {
    NSString *path = executablePath();
    NSRange marker = [path rangeOfString:@"/Contents/MacOS/"];
    if (marker.location == NSNotFound) return @"";
    return [path substringToIndex:marker.location];
}

static pid_t gWorkerPID = 0;

static void forwardSignal(int sig) {
    if (gWorkerPID > 0) kill(gWorkerPID, sig);
}

// TCC attributes a spawned CLI tool to its "responsible process" (the
// terminal, agent or obu launcher), so a grant to this helper would otherwise
// be ignored. Re-exec once with responsibility disclaimed so macOS evaluates
// Accessibility against this signed helper bundle only.
static BOOL runDisclaimed(int argc, const char *argv[], int *exitCode) {
    if (getenv(kDisclaimedEnv.UTF8String) != NULL) return NO;
    typedef int (*DisclaimFn)(posix_spawnattr_t *, int);
    DisclaimFn disclaim = (DisclaimFn)dlsym(RTLD_DEFAULT, "responsibility_spawnattrs_setdisclaim");
    if (disclaim == NULL) return NO;

    NSString *path = executablePath();
    if (path.length == 0) return NO;

    posix_spawnattr_t attr;
    if (posix_spawnattr_init(&attr) != 0) return NO;
    if (disclaim(&attr, 1) != 0) {
        posix_spawnattr_destroy(&attr);
        return NO;
    }

    size_t envCount = 0;
    while (environ[envCount] != NULL) envCount++;
    char **envp = calloc(envCount + 2, sizeof(char *));
    char **childArgv = calloc((size_t)argc + 1, sizeof(char *));
    if (envp == NULL || childArgv == NULL) {
        free(envp);
        free(childArgv);
        posix_spawnattr_destroy(&attr);
        return NO;
    }
    for (size_t i = 0; i < envCount; i++) envp[i] = environ[i];
    NSString *marker = [kDisclaimedEnv stringByAppendingString:@"=1"];
    envp[envCount] = (char *)marker.UTF8String;
    childArgv[0] = (char *)path.UTF8String;
    for (int i = 1; i < argc; i++) childArgv[i] = (char *)argv[i];

    pid_t child = 0;
    int spawnError = posix_spawn(&child, path.UTF8String, NULL, &attr, childArgv, envp);
    posix_spawnattr_destroy(&attr);
    free(envp);
    free(childArgv);
    if (spawnError != 0) return NO;

    gWorkerPID = child;
    signal(SIGTERM, forwardSignal);
    signal(SIGINT, forwardSignal);
    signal(SIGHUP, forwardSignal);
    int status = 0;
    while (waitpid(child, &status, 0) == -1) {
        if (errno != EINTR) {
            *exitCode = kExitNotFound;
            return YES;
        }
    }
    *exitCode = WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
    return YES;
}

#pragma mark - State directory, locks, permission

static NSString *stateDirectory(void) {
    NSString *dir = [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Application Support/OpenBrowserUse/approval-helper"];
    [[NSFileManager defaultManager] createDirectoryAtPath:dir withIntermediateDirectories:YES attributes:nil error:NULL];
    return dir;
}

// Returns a held lock fd, -1 when another helper owns the lock, or -2 when
// locking is unavailable (the caller proceeds unlocked rather than failing).
static int acquireLock(NSString *name) {
    NSString *path = [stateDirectory() stringByAppendingPathComponent:name];
    int fd = open(path.fileSystemRepresentation, O_CREAT | O_RDWR | O_CLOEXEC, 0600);
    if (fd < 0) return -2;
    if (flock(fd, LOCK_EX | LOCK_NB) == 0) return fd;
    int lockErrno = errno;
    close(fd);
    return lockErrno == EWOULDBLOCK ? -1 : -2;
}

static void releaseLock(int fd) {
    if (fd >= 0) close(fd);
}

static BOOL accessibilityTrusted(BOOL prompt) {
    const void *keys[] = { kAXTrustedCheckOptionPrompt };
    const void *values[] = { prompt ? kCFBooleanTrue : kCFBooleanFalse };
    CFDictionaryRef options = CFDictionaryCreate(
        NULL, keys, values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    BOOL trusted = AXIsProcessTrustedWithOptions(options);
    CFRelease(options);
    return trusted;
}

// Background operations never show blocking UI. The system prompt is asked
// for at most once per helper build so a missing grant is discoverable
// without a prompt per request.
static BOOL backgroundPermission(void) {
    if (accessibilityTrusted(NO)) return YES;
    struct stat info;
    NSString *exe = executablePath();
    NSString *build = @"unknown";
    if (stat(exe.fileSystemRepresentation, &info) == 0) {
        build = [NSString stringWithFormat:@"%llu-%ld", (unsigned long long)info.st_ino, (long)info.st_mtimespec.tv_sec];
    }
    NSString *marker = [stateDirectory() stringByAppendingPathComponent:[@"prompted-" stringByAppendingString:build]];
    int fd = open(marker.fileSystemRepresentation, O_CREAT | O_EXCL | O_WRONLY | O_CLOEXEC, 0600);
    if (fd >= 0) {
        close(fd);
        accessibilityTrusted(YES);
    }
    fprintf(stderr, "open-browser-use approval helper: macOS Accessibility permission is missing; run \"%s\" setup\n",
            exe.UTF8String);
    return NO;
}

static void openAccessibilitySettings(void) {
    NSURL *url = [NSURL URLWithString:@"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"];
    [[NSWorkspace sharedWorkspace] openURL:url];
}

static int runSetup(void) {
    [NSApplication sharedApplication];
    NSString *bundlePath = helperBundlePath();
    if (bundlePath.length > 0) {
        LSRegisterURL((__bridge CFURLRef)[NSURL fileURLWithPath:bundlePath], true);
    }
    if (accessibilityTrusted(YES)) {
        printf("trusted=1\n");
        return kExitDone;
    }
    openAccessibilitySettings();
    if (bundlePath.length > 0) {
        // Open the containing folder in Finder explicitly: selectFile: honours
        // the NSFileViewer default, which may name an uninstalled file manager.
        NSURL *folder = [NSURL fileURLWithPath:bundlePath.stringByDeletingLastPathComponent isDirectory:YES];
        NSURL *finder = [NSURL fileURLWithPath:@"/System/Library/CoreServices/Finder.app" isDirectory:YES];
        [[NSWorkspace sharedWorkspace] openURLs:@[ folder ]
                           withApplicationAtURL:finder
                                  configuration:[NSWorkspaceOpenConfiguration configuration]
                              completionHandler:nil];
        printf("helper=%s\n", bundlePath.UTF8String);
    }
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    [NSApp activateIgnoringOtherApps:YES];
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"Open Browser Use 需要辅助功能权限";
    alert.informativeText = @"请在“系统设置 → 隐私与安全性 → 辅助功能”中启用 Open Browser Use Approval Helper。列表中没有时点击“+”，选择刚打开的 Finder 窗口中的 Open Browser Use Approval Helper.app；也可在选择框中按 ⌘⇧G 粘贴 helper 所在目录。启用后重新运行 OBU。";
    [alert addButtonWithTitle:@"打开辅助功能设置"];
    [alert addButtonWithTitle:@"关闭"];
    if ([alert runModal] == NSAlertFirstButtonReturn) openAccessibilitySettings();
    printf("trusted=0\n");
    return kExitNoPermission;
}

#pragma mark - Diagnostics log

static NSString *gOperation = @"";

static void helperLog(NSString *event, NSDictionary *fields) {
    NSString *path = nil;
    const char *override = getenv("OBU_LOG_FILE");
    if (override != NULL && *override != '\0') {
        path = [NSString stringWithUTF8String:override];
    } else {
        path = [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Logs/OpenBrowserUse/native-host.log"];
    }
    [[NSFileManager defaultManager] createDirectoryAtPath:path.stringByDeletingLastPathComponent
                              withIntermediateDirectories:YES
                                               attributes:nil
                                                    error:NULL];
    NSMutableDictionary *record = [NSMutableDictionary dictionaryWithDictionary:fields ?: @{}];
    NSISO8601DateFormatter *formatter = [NSISO8601DateFormatter new];
    formatter.formatOptions = NSISO8601DateFormatWithInternetDateTime | NSISO8601DateFormatWithFractionalSeconds;
    record[@"ts"] = [formatter stringFromDate:[NSDate date]];
    record[@"event"] = event;
    record[@"process"] = @"approval-helper";
    record[@"pid"] = @(getpid());
    record[@"operation"] = gOperation;
    const char *reason = getenv("OBU_MAC_APPROVAL_REASON");
    if (reason != NULL && *reason != '\0') record[@"reason"] = [NSString stringWithUTF8String:reason];
    NSData *json = [NSJSONSerialization dataWithJSONObject:record options:0 error:NULL];
    if (json == nil) return;
    NSMutableData *line = [json mutableCopy];
    [line appendBytes:"\n" length:1];
    int fd = open(path.fileSystemRepresentation, O_CREAT | O_WRONLY | O_APPEND | O_CLOEXEC, 0644);
    if (fd < 0) return;
    ssize_t ignored = write(fd, line.bytes, line.length);
    (void)ignored;
    close(fd);
}

#pragma mark - Accessibility helpers

static NSString *axString(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue(element, attribute, &value) != kAXErrorSuccess || value == NULL) {
        if (value != NULL) CFRelease(value);
        return @"";
    }
    NSString *result = @"";
    if (CFGetTypeID(value) == CFStringGetTypeID()) result = [(__bridge NSString *)value copy];
    CFRelease(value);
    return result;
}

static CFArrayRef copyAXArray(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue(element, attribute, &value) != kAXErrorSuccess || value == NULL) {
        if (value != NULL) CFRelease(value);
        return NULL;
    }
    if (CFGetTypeID(value) != CFArrayGetTypeID()) {
        CFRelease(value);
        return NULL;
    }
    return (CFArrayRef)value;
}

static AXUIElementRef copyAXElement(AXUIElementRef element, CFStringRef attribute) {
    CFTypeRef value = NULL;
    if (AXUIElementCopyAttributeValue(element, attribute, &value) != kAXErrorSuccess || value == NULL) {
        if (value != NULL) CFRelease(value);
        return NULL;
    }
    if (CFGetTypeID(value) != AXUIElementGetTypeID()) {
        CFRelease(value);
        return NULL;
    }
    return (AXUIElementRef)value;
}

static BOOL hasRole(AXUIElementRef element, CFStringRef role) {
    return [axString(element, kAXRoleAttribute) isEqualToString:(__bridge NSString *)role];
}

static BOOL isWebArea(AXUIElementRef element) {
    return [axString(element, kAXRoleAttribute) isEqualToString:@"AXWebArea"];
}

static BOOL textMatches(AXUIElementRef element, BOOL (*predicate)(NSString *)) {
    return predicate(axString(element, kAXTitleAttribute)) ||
           predicate(axString(element, kAXDescriptionAttribute)) ||
           predicate(axString(element, kAXValueAttribute));
}

// All tree walks below stop at AXWebArea: page content can imitate browser
// dialogs and must never be clicked by this helper.
static BOOL subtreeHasText(AXUIElementRef element, BOOL (*predicate)(NSString *), NSUInteger depth, NSUInteger maxDepth) {
    if (element == NULL || depth > maxDepth || isWebArea(element)) return NO;
    if (textMatches(element, predicate)) return YES;
    CFArrayRef children = copyAXArray(element, kAXChildrenAttribute);
    if (children == NULL) return NO;
    BOOL found = NO;
    for (CFIndex i = 0; i < CFArrayGetCount(children) && !found; i++) {
        found = subtreeHasText((AXUIElementRef)CFArrayGetValueAtIndex(children, i), predicate, depth + 1, maxDepth);
    }
    CFRelease(children);
    return found;
}

static AXUIElementRef copyButtonInSubtree(AXUIElementRef element, BOOL (*label)(NSString *), NSUInteger depth, NSUInteger maxDepth) {
    if (element == NULL || depth > maxDepth || isWebArea(element)) return NULL;
    if (hasRole(element, kAXButtonRole) &&
        (label(axString(element, kAXTitleAttribute)) || label(axString(element, kAXDescriptionAttribute)))) {
        return (AXUIElementRef)CFRetain(element);
    }
    CFArrayRef children = copyAXArray(element, kAXChildrenAttribute);
    if (children == NULL) return NULL;
    AXUIElementRef button = NULL;
    for (CFIndex i = 0; i < CFArrayGetCount(children) && button == NULL; i++) {
        button = copyButtonInSubtree((AXUIElementRef)CFArrayGetValueAtIndex(children, i), label, depth + 1, maxDepth);
    }
    CFRelease(children);
    return button;
}

// Presses the first matching button. Returns kAXErrorNoValue when no button
// matches; any other non-success value is the AXPress failure.
static AXError pressButtonInSubtree(AXUIElementRef element, BOOL (*label)(NSString *), NSUInteger maxDepth) {
    AXUIElementRef button = copyButtonInSubtree(element, label, 0, maxDepth);
    if (button == NULL) return kAXErrorNoValue;
    AXError error = AXUIElementPerformAction(button, kAXPressAction);
    CFRelease(button);
    return error;
}

static AXUIElementRef copyFirstTextMatch(AXUIElementRef element, BOOL (*predicate)(NSString *), NSUInteger depth, NSUInteger maxDepth) {
    if (element == NULL || depth > maxDepth || isWebArea(element)) return NULL;
    if (textMatches(element, predicate)) return (AXUIElementRef)CFRetain(element);
    CFArrayRef children = copyAXArray(element, kAXChildrenAttribute);
    if (children == NULL) return NULL;
    AXUIElementRef match = NULL;
    for (CFIndex i = 0; i < CFArrayGetCount(children) && match == NULL; i++) {
        match = copyFirstTextMatch((AXUIElementRef)CFArrayGetValueAtIndex(children, i), predicate, depth + 1, maxDepth);
    }
    CFRelease(children);
    return match;
}

#pragma mark - Approval and banner

// Browser-native dialog candidates only: sheets attached to a window and
// standalone dialog windows. Web content is never a candidate.
static NSArray *approvalCandidates(AXUIElementRef window) {
    NSMutableArray *candidates = [NSMutableArray array];
    NSString *subrole = axString(window, kAXSubroleAttribute);
    if ([subrole isEqualToString:(__bridge NSString *)kAXDialogSubrole] ||
        [subrole isEqualToString:(__bridge NSString *)kAXSystemDialogSubrole]) {
        [candidates addObject:(__bridge id)window];
    }
    CFArrayRef children = copyAXArray(window, kAXChildrenAttribute);
    if (children != NULL) {
        for (CFIndex i = 0; i < CFArrayGetCount(children); i++) {
            AXUIElementRef child = (AXUIElementRef)CFArrayGetValueAtIndex(children, i);
            if (hasRole(child, kAXSheetRole)) [candidates addObject:(__bridge id)child];
        }
        CFRelease(children);
    }
    return candidates;
}

typedef NS_ENUM(NSInteger, ScanResult) {
    ScanNothing = 0,   // no browser-native approval sheet visible
    ScanVisible = 1,   // sheet visible, Allow not pressed (yet)
    ScanPressed = 2,
};

static BOOL gDryRun = NO;
static AXError gLastPressError = kAXErrorSuccess;

static ScanResult approveInApplication(AXUIElementRef application) {
    CFArrayRef windows = copyAXArray(application, kAXWindowsAttribute);
    if (windows == NULL) return ScanNothing;
    ScanResult result = ScanNothing;
    for (CFIndex w = 0; w < CFArrayGetCount(windows) && result != ScanPressed; w++) {
        for (id candidate in approvalCandidates((AXUIElementRef)CFArrayGetValueAtIndex(windows, w))) {
            AXUIElementRef dialog = (__bridge AXUIElementRef)candidate;
            if (!subtreeHasText(dialog, isApprovalText, 0, kMaxSheetDepth)) continue;
            result = ScanVisible;
            if (gDryRun) {
                AXUIElementRef button = copyButtonInSubtree(dialog, isAllowLabel, 0, kMaxSheetDepth);
                printf("approval sheet: window=%ld role=%s allow_button=%s\n", (long)w,
                       axString(dialog, kAXRoleAttribute).UTF8String, button != NULL ? "found" : "missing");
                if (button != NULL) CFRelease(button);
                continue;
            }
            AXError error = pressButtonInSubtree(dialog, isAllowLabel, kMaxSheetDepth);
            if (error == kAXErrorSuccess) {
                helperLog(@"mac_approval_helper_pressed", @{ @"window" : @(w), @"control" : @"allow" });
                result = ScanPressed;
                break;
            }
            if (error != gLastPressError) {  // log each distinct failure once
                gLastPressError = error;
                helperLog(@"mac_approval_helper_press_failed", @{ @"window" : @(w), @"axError" : @(error) });
            }
        }
    }
    CFRelease(windows);
    return result;
}

// Find the automation text, then climb to the nearest ancestor that owns a
// close button: that ancestor is this infobar. Stop at the shared infobar
// container so another infobar's close button is never pressed.
static BOOL dismissInWindow(AXUIElementRef window) {
    AXUIElementRef current = copyFirstTextMatch(window, isBannerMarkerText, 0, kMaxWindowDepth);
    BOOL dismissed = NO;
    for (int level = 0; current != NULL && level < 6 && !dismissed; level++) {
        if (textMatches(current, isInfoBarContainerLabel) || hasRole(current, kAXWindowRole)) break;
        if (gDryRun) {
            AXUIElementRef button = copyButtonInSubtree(current, isCloseLabel, 0, 4);
            if (button != NULL) {
                printf("automation banner: close_button=found level=%d\n", level);
                CFRelease(button);
                break;
            }
        } else {
            AXError error = pressButtonInSubtree(current, isCloseLabel, 4);
            if (error == kAXErrorSuccess) {
                helperLog(@"mac_approval_helper_pressed", @{ @"control" : @"banner_close", @"level" : @(level) });
                dismissed = YES;
                break;
            }
            if (error != kAXErrorNoValue) {
                helperLog(@"mac_approval_helper_press_failed", @{ @"control" : @"banner_close", @"axError" : @(error) });
                break;
            }
        }
        AXUIElementRef parent = copyAXElement(current, kAXParentAttribute);
        CFRelease(current);
        current = parent;
    }
    if (current != NULL) CFRelease(current);
    return dismissed;
}

static BOOL dismissInApplication(AXUIElementRef application) {
    CFArrayRef windows = copyAXArray(application, kAXWindowsAttribute);
    if (windows == NULL) return NO;
    BOOL dismissed = NO;
    for (CFIndex w = 0; w < CFArrayGetCount(windows) && !dismissed; w++) {
        dismissed = dismissInWindow((AXUIElementRef)CFArrayGetValueAtIndex(windows, w));
    }
    CFRelease(windows);
    return dismissed;
}

static BOOL isBrowser(NSRunningApplication *application) {
    return isBrowserBundleID(application.bundleIdentifier);
}

static NSArray<NSRunningApplication *> *runningBrowsers(void) {
    NSMutableArray *browsers = [NSMutableArray array];
    for (NSRunningApplication *application in [NSWorkspace sharedWorkspace].runningApplications) {
        if (isBrowser(application)) [browsers addObject:application];
    }
    return browsers;
}

static AXUIElementRef createAXApplication(NSRunningApplication *application) {
    AXUIElementRef axApplication = AXUIElementCreateApplication(application.processIdentifier);
    if (axApplication != NULL) AXUIElementSetMessagingTimeout(axApplication, 1.0f);
    return axApplication;
}

static ScanResult approveOnce(void) {
    ScanResult best = ScanNothing;
    for (NSRunningApplication *application in runningBrowsers()) {
        AXUIElementRef axApplication = createAXApplication(application);
        if (axApplication == NULL) continue;
        ScanResult result = approveInApplication(axApplication);
        CFRelease(axApplication);
        if (result > best) best = result;
        if (best == ScanPressed) break;
    }
    return best;
}

static BOOL dismissOnce(void) {
    for (NSRunningApplication *application in runningBrowsers()) {
        AXUIElementRef axApplication = createAXApplication(application);
        if (axApplication == NULL) continue;
        BOOL dismissed = dismissInApplication(axApplication);
        CFRelease(axApplication);
        if (dismissed) return YES;
    }
    return NO;
}

// A caller that exits only ends the wait while no sheet is visible: once
// Chrome shows the sheet for a request, the helper finishes approving it so
// the daemon-wide Browser Root handshake still completes for later callers.
static int runApproveWait(NSTimeInterval seconds) {
    int lock = acquireLock(@"approve.lock");
    if (lock == -1) {
        helperLog(@"mac_approval_helper_finished", @{ @"phase" : @"approve", @"status" : @"already_running" });
        return kExitBusy;
    }
    pid_t caller = callerPID();
    helperLog(@"mac_approval_helper_phase_started", @{ @"phase" : @"approve", @"callerPID" : @(caller), @"seconds" : @(seconds) });
    NSDate *started = [NSDate date];
    NSDate *deadline = [started dateByAddingTimeInterval:seconds];
    int rc = kExitNotFound;
    BOOL sawSheet = NO;
    while ([deadline timeIntervalSinceNow] > 0.0) {
        ScanResult result;
        @autoreleasepool {
            result = approveOnce();
        }
        if (result == ScanPressed) {
            rc = kExitDone;
            break;
        }
        if (result == ScanVisible && !sawSheet) {
            sawSheet = YES;
            helperLog(@"mac_approval_helper_sheet_visible", @{ @"afterMs" : @((long)(-[started timeIntervalSinceNow] * 1000)) });
        }
        if (result == ScanNothing && !callerAlive(caller)) {
            rc = kExitCallerGone;
            break;
        }
        usleep(kPollInterval);
    }
    releaseLock(lock);
    helperLog(@"mac_approval_helper_finished", @{
        @"phase" : @"approve",
        @"exitCode" : @(rc),
        @"sawSheet" : @(sawSheet),
        @"elapsedMs" : @((long)(-[started timeIntervalSinceNow] * 1000)),
    });
    return rc;
}

// The banner belongs to Chrome, not to the caller: keep waiting after the
// caller exits because Chrome renders the infobar shortly after approval.
static int runDismissWait(NSTimeInterval seconds) {
    int lock = acquireLock(@"dismiss.lock");
    if (lock == -1) return kExitBusy;
    NSDate *started = [NSDate date];
    NSDate *deadline = [started dateByAddingTimeInterval:seconds];
    int rc = kExitNotFound;
    while ([deadline timeIntervalSinceNow] > 0.0) {
        BOOL dismissed;
        @autoreleasepool {
            dismissed = dismissOnce();
        }
        if (dismissed) {
            rc = kExitDone;
            break;
        }
        usleep(kPollInterval);
    }
    releaseLock(lock);
    helperLog(@"mac_approval_helper_finished", @{
        @"phase" : @"dismiss_banner",
        @"exitCode" : @(rc),
        @"elapsedMs" : @((long)(-[started timeIntervalSinceNow] * 1000)),
    });
    return rc;
}

#pragma mark - Diagnostics

static NSString *clip(NSString *text) {
    NSString *flat = [[text componentsSeparatedByCharactersInSet:[NSCharacterSet newlineCharacterSet]] componentsJoinedByString:@" "];
    return flat.length > 80 ? [[flat substringToIndex:80] stringByAppendingString:@"…"] : flat;
}

static void dumpElement(AXUIElementRef element, NSUInteger depth) {
    if (depth > kMaxWindowDepth) return;
    NSString *role = axString(element, kAXRoleAttribute);
    NSString *subrole = axString(element, kAXSubroleAttribute);
    NSMutableString *line = [NSMutableString stringWithString:[@"" stringByPaddingToLength:depth * 2 withString:@" " startingAtIndex:0]];
    [line appendString:role.length ? role : @"?"];
    if (subrole.length) [line appendFormat:@"/%@", subrole];
    for (NSArray *pair in @[ @[ @"title", (__bridge NSString *)kAXTitleAttribute ],
                             @[ @"desc", (__bridge NSString *)kAXDescriptionAttribute ],
                             @[ @"value", (__bridge NSString *)kAXValueAttribute ] ]) {
        NSString *text = axString(element, (__bridge CFStringRef)pair[1]);
        if (text.length) [line appendFormat:@" %@=\"%@\"", pair[0], clip(text)];
    }
    if (isWebArea(element)) {
        [line appendString:@" (web content skipped)"];
        printf("%s\n", line.UTF8String);
        return;
    }
    printf("%s\n", line.UTF8String);
    CFArrayRef children = copyAXArray(element, kAXChildrenAttribute);
    if (children == NULL) return;
    for (CFIndex i = 0; i < CFArrayGetCount(children); i++) {
        dumpElement((AXUIElementRef)CFArrayGetValueAtIndex(children, i), depth + 1);
    }
    CFRelease(children);
}

static void dumpBrowsers(void) {
    for (NSRunningApplication *application in runningBrowsers()) {
        AXUIElementRef axApplication = createAXApplication(application);
        if (axApplication == NULL) continue;
        printf("=== %s (pid %d)\n", (application.bundleIdentifier ?: @"?").UTF8String, application.processIdentifier);
        CFArrayRef windows = copyAXArray(axApplication, kAXWindowsAttribute);
        if (windows != NULL) {
            for (CFIndex w = 0; w < CFArrayGetCount(windows); w++) {
                printf("--- window %ld\n", (long)w);
                dumpElement((AXUIElementRef)CFArrayGetValueAtIndex(windows, w), 0);
            }
            CFRelease(windows);
        }
        CFRelease(axApplication);
    }
}

#define EXPECT(cond)                                                    \
    do {                                                                \
        if (!(cond)) {                                                  \
            fprintf(stderr, "self-test failed: %s\n", #cond);           \
            failures++;                                                 \
        }                                                               \
    } while (0)

static int runSelfTest(void) {
    int failures = 0;
    EXPECT(isApprovalText(@"Allow remote debugging?"));
    EXPECT(isApprovalText(@"要允许远程调试吗？"));
    EXPECT(isApprovalText(@"Another app wants to use Chrome remote debugging"));
    EXPECT(!isApprovalText(@""));
    EXPECT(!isApprovalText(@"Allow notifications?"));
    EXPECT(isAllowLabel(@"Allow"));
    EXPECT(isAllowLabel(@"允许"));
    EXPECT(!isAllowLabel(@"Allow all"));
    EXPECT(!isAllowLabel(@"Don't allow"));
    EXPECT(!isAllowLabel(@"不允许"));
    EXPECT(isBannerMarkerText(@"Chrome is being controlled by automated test software."));
    EXPECT(isBannerMarkerText(@"自动测试软件正在控制 Chrome"));
    EXPECT(isBannerMarkerText(@"Chrome 正受到自动测试软件的控制。"));
    EXPECT(!isBannerMarkerText(@"Restore pages?"));
    EXPECT(isCloseLabel(@"关闭"));
    EXPECT(isCloseLabel(@"Close"));
    EXPECT(!isCloseLabel(@"在“设置”中关闭"));
    EXPECT(!isCloseLabel(@"Turn off in Settings"));
    EXPECT(isInfoBarContainerLabel(@"信息栏容器"));
    EXPECT(!isInfoBarContainerLabel(@"Info bar"));
    EXPECT(isBrowserBundleID(@"com.google.Chrome"));
    EXPECT(isBrowserBundleID(@"com.google.Chrome.canary"));
    EXPECT(isBrowserBundleID(@"com.google.Chrome.app.fmpnliohjhemenmnlpbfagaolkdacoja"));
    EXPECT(isBrowserBundleID(@"org.chromium.Chromium.app.abc"));
    EXPECT(!isBrowserBundleID(@"com.google.Chrome.app."));
    EXPECT(!isBrowserBundleID(@"com.google.ChromeRemoteDesktop"));
    EXPECT(!isBrowserBundleID(@"com.apple.Safari"));
    EXPECT(!isBrowserBundleID(nil));
    EXPECT(parseSeconds("15", 30, 120) == 15);
    EXPECT(parseSeconds("2.5", 30, 120) == 2.5);
    EXPECT(parseSeconds("abc", 30, 120) == 30);
    EXPECT(parseSeconds("0", 30, 120) == 30);
    EXPECT(parseSeconds("500", 30, 120) == 30);
    EXPECT(parseSeconds(NULL, 30, 120) == 30);
    EXPECT(callerAlive(0));
    EXPECT(callerAlive(getpid()));
    pid_t child = fork();
    if (child == 0) _exit(0);
    if (child > 0) {
        waitpid(child, NULL, 0);
        EXPECT(!callerAlive(child));
    }
    setenv("OBU_MAC_APPROVAL_PARENT_PID", "4242", 1);
    EXPECT(callerPID() == 4242);
    setenv("OBU_MAC_APPROVAL_PARENT_PID", "1", 1);
    EXPECT(callerPID() == 0);
    setenv("OBU_MAC_APPROVAL_PARENT_PID", "12x", 1);
    EXPECT(callerPID() == 0);
    unsetenv("OBU_MAC_APPROVAL_PARENT_PID");
    if (failures == 0) printf("self-test ok\n");
    return failures == 0 ? kExitDone : kExitNotFound;
}

#pragma mark - Entry point

int main(int argc, const char *argv[]) {
    @autoreleasepool {
        // Launchers may stop reading stderr once they exit; a late
        // diagnostic must not kill the helper.
        signal(SIGPIPE, SIG_IGN);
        if (argc < 2) return kExitUsage;
        NSString *operation = [NSString stringWithUTF8String:argv[1]];
        if ([operation isEqualToString:@"self-test"]) return runSelfTest();

        NSArray *operations = @[ @"approve-then-dismiss-wait", @"approve-wait", @"dismiss-banner-wait", @"approve",
                                 @"dismiss-banner", @"status", @"setup", @"dump", @"probe" ];
        if (![operations containsObject:operation]) return kExitUsage;

        int disclaimedExit = 0;
        if (runDisclaimed(argc, argv, &disclaimedExit)) return disclaimedExit;
        gOperation = operation;

        if ([operation isEqualToString:@"setup"]) return runSetup();
        if ([operation isEqualToString:@"status"]) {
            BOOL trusted = accessibilityTrusted(NO);
            printf("trusted=%d\n", trusted ? 1 : 0);
            return trusted ? kExitDone : kExitNoPermission;
        }
        if (!backgroundPermission()) {
            helperLog(@"mac_approval_helper_finished", @{ @"status" : @"permission_denied", @"exitCode" : @(kExitNoPermission) });
            return kExitNoPermission;
        }

        const char *first = argc >= 3 ? argv[2] : NULL;
        const char *second = argc >= 4 ? argv[3] : NULL;
        if ([operation isEqualToString:@"dump"]) {
            dumpBrowsers();
            return kExitDone;
        }
        if ([operation isEqualToString:@"probe"]) {
            gDryRun = YES;
            BOOL sheet = approveOnce() != ScanNothing;
            dismissOnce();
            if (!sheet) printf("approval sheet: none\n");
            return kExitDone;
        }
        if ([operation isEqualToString:@"approve"]) return approveOnce() == ScanPressed ? kExitDone : kExitNotFound;
        if ([operation isEqualToString:@"dismiss-banner"]) return dismissOnce() ? kExitDone : kExitNotFound;
        if ([operation isEqualToString:@"approve-wait"]) return runApproveWait(parseSeconds(first, 30, 300));
        if ([operation isEqualToString:@"dismiss-banner-wait"]) return runDismissWait(parseSeconds(first, 15, 120));

        // approve-then-dismiss-wait: exit status reports the approval phase;
        // banner dismissal is best effort and never changes it.
        int rc = runApproveWait(parseSeconds(first, 30, 300));
        if (rc == kExitDone) runDismissWait(parseSeconds(second, 15, 120));
        return rc;
    }
}
