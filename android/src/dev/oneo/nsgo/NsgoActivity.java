package dev.oneo.nsgo;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.res.Configuration;
import android.content.MutableContextWrapper;
import android.net.LocalSocket;
import android.os.Build;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.Surface;
import android.view.Window;
import android.view.WindowManager;

/** The NSGo UI attaches to the retained runtime owned by PlaybackService. */
public class NsgoActivity extends Activity {
    private NsgoView view;
    private AndroidHostDev devHost;
    private Integer previousOrientation;

    @Override public void onCreate(Bundle state) {
        int night = getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK;
        if (night != Configuration.UI_MODE_NIGHT_YES) setTheme(android.R.style.Theme_DeviceDefault_Light_NoActionBar);
        super.onCreate(state);
        Window w = getWindow();
        WindowManager.LayoutParams lp = w.getAttributes();
        lp.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
        w.setAttributes(lp);
        w.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        w.setStatusBarColor(android.graphics.Color.TRANSPARENT);
        w.setNavigationBarColor(android.graphics.Color.TRANSPARENT);
        if (Build.VERSION.SDK_INT >= 30) w.setDecorFitsSystemWindows(false);
        devHost = AndroidHostDev.open(this);
        if (devHost != null) return;
        view = PlaybackRuntime.attach(this);
        view.setFlags(getIntent().getExtras());
        view.setTest(getIntent().getStringExtra(TestAgent.EXTRA));
        setContentView(view);
    }

    /** A new Go app gets a fresh view within the Activity's existing window. */
    void showHostEngine(LocalSocket socket) {
        if (view != null) view.stopHostView();
        requestOrientation(0);
        NsgoView next = new NsgoView(new MutableContextWrapper(this));
        next.setHostSocket(socket);
        view = next;
        setContentView(next);
    }

    /** Applies the app's orientation request until it releases its last scope. */
    void requestOrientation(int mask) {
        int next;
        if (mask == 0) {
            if (previousOrientation == null) return;
            next = previousOrientation;
            previousOrientation = null;
        } else {
            if (previousOrientation == null) previousOrientation = getRequestedOrientation();
            next = orientationMode(mask);
        }
        if (getRequestedOrientation() != next) setRequestedOrientation(next);
    }

    private int orientationMode(int mask) {
        final int portrait = Protocol.ORIENTATION_PORTRAIT;
        final int upsideDown = Protocol.ORIENTATION_PORTRAIT_UPSIDE_DOWN;
        final int left = Protocol.ORIENTATION_LANDSCAPE_LEFT;
        final int right = Protocol.ORIENTATION_LANDSCAPE_RIGHT;
        final int bothPortrait = portrait | upsideDown;
        final int all = bothPortrait | Protocol.ORIENTATION_LANDSCAPE;
        switch (mask) {
            case bothPortrait: return ActivityInfo.SCREEN_ORIENTATION_SENSOR_PORTRAIT;
            case Protocol.ORIENTATION_LANDSCAPE: return ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE;
            case all: return ActivityInfo.SCREEN_ORIENTATION_FULL_USER;
        }
        // Android cannot express every mixed set. Keep the current orientation
        // if it is allowed, else lock to an allowed one in the same axis when possible.
        boolean upright = getResources().getConfiguration().orientation == Configuration.ORIENTATION_PORTRAIT;
        int rotation = getWindowManager().getDefaultDisplay().getRotation();
        boolean reverse = rotation == Surface.ROTATION_180 || rotation == Surface.ROTATION_270;
        int current = upright ? (reverse ? upsideDown : portrait) : (reverse ? right : left);
        int sameAxis = mask & (upright ? bothPortrait : Protocol.ORIENTATION_LANDSCAPE);
        int chosen = (current & mask) != 0 ? current : Integer.lowestOneBit(sameAxis != 0 ? sameAxis : mask);
        switch (chosen) {
            case portrait: return ActivityInfo.SCREEN_ORIENTATION_PORTRAIT;
            case upsideDown: return ActivityInfo.SCREEN_ORIENTATION_REVERSE_PORTRAIT;
            case left: return ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE;
            case right: return ActivityInfo.SCREEN_ORIENTATION_REVERSE_LANDSCAPE;
            default: return ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED;
        }
    }

    @Override protected void onStop() {
        if (devHost == null) PlaybackRuntime.lifecycle();
        super.onStop();
    }

    @Override protected void onDestroy() {
        if (devHost == null) {
            PlaybackRuntime.detach(this);
        } else {
            devHost.close();
            if (view != null) view.stopHostView();
        }
        super.onDestroy();
    }

    @Override public void onBackPressed() { if (view == null || !view.back()) super.onBackPressed(); }

    @Override protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (view != null) view.activityResult(request, result, data);
    }

    @Override public void onRequestPermissionsResult(int request, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(request, permissions, results);
        if (view != null) view.permissionResult(request, permissions, results);
    }

    @Override public boolean onKeyDown(int key, KeyEvent event) {
        String command = devHost == null ? mediaCommand(key) : null;
        if (command != null) {
            if (event.getRepeatCount() == 0) PlaybackRuntime.command(command, 0);
            return true;
        }
        return super.onKeyDown(key, event);
    }

    @Override public boolean onKeyUp(int key, KeyEvent event) {
        return (devHost == null && mediaCommand(key) != null) || super.onKeyUp(key, event);
    }

    private String mediaCommand(int key) {
        switch (key) {
            case KeyEvent.KEYCODE_MEDIA_PLAY: return "play";
            case KeyEvent.KEYCODE_MEDIA_PAUSE: return "pause";
            case KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE:
            case KeyEvent.KEYCODE_HEADSETHOOK: return "toggle";
            case KeyEvent.KEYCODE_MEDIA_NEXT: return "next";
            case KeyEvent.KEYCODE_MEDIA_PREVIOUS: return "previous";
            case KeyEvent.KEYCODE_MEDIA_STOP: return "stop";
            default: return null;
        }
    }

    void followAppearance(boolean dark) {
        int theme = dark ? android.R.style.Theme_DeviceDefault_NoActionBar : android.R.style.Theme_DeviceDefault_Light_NoActionBar;
        setTheme(theme);
        getTheme().applyStyle(theme, true);
        android.content.res.TypedArray a = getTheme().obtainStyledAttributes(new int[]{android.R.attr.windowBackground});
        getWindow().setBackgroundDrawable(a.getDrawable(0));
        a.recycle();
    }
}
