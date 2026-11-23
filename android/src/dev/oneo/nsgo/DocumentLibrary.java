package dev.oneo.nsgo;

import android.app.Activity;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract;
import android.util.Base64;
import android.util.Log;
import java.io.ByteArrayOutputStream;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Supplier;

/**
 * Bridges Android Storage Access Framework (SAF) persisted document trees to
 * Wood & Wind Music's Go library over NSGo's public host channel. Audio files
 * stay in place and stream directly via content:// URIs without copying.
 */
final class DocumentLibrary {
    private static final String TAG = "wood wind music";
    static final int PICK_TREE_REQUEST = 0x776d;
    private static final int MAX_CHUNK_BYTES = 256 * 1024;
    private static final int MAX_READ_FILE_BYTES = 16 * 1024 * 1024;
    private static final int MAX_OPEN_DOCS = 64;
    private static final long IDLE_DOC_MS = 60_000L;

    private static final ExecutorService io = Executors.newFixedThreadPool(4, r -> {
        Thread t = new Thread(r, "woodwind-saf");
        t.setDaemon(true);
        return t;
    });
    private static final AtomicInteger nextHandle = new AtomicInteger(1);
    private static final ConcurrentHashMap<Integer, OpenDoc> openDocs = new ConcurrentHashMap<>();
    private static NsgoView.HostReply pendingPick;

    private static final class OpenDoc {
        final Uri uri;
        final ParcelFileDescriptor pfd;
        final FileInputStream stream;
        final FileChannel channel;
        final long size;
        volatile long lastUsedMs;

        OpenDoc(Uri uri, ParcelFileDescriptor pfd, FileInputStream stream, FileChannel channel, long size) {
            this.uri = uri;
            this.pfd = pfd;
            this.stream = stream;
            this.channel = channel;
            this.size = size;
            this.lastUsedMs = System.currentTimeMillis();
        }

        synchronized byte[] readAt(ContentResolver resolver, long offset, int length) throws IOException {
            lastUsedMs = System.currentTimeMillis();
            if (offset < 0 || length <= 0) return new byte[0];
            if (channel != null) {
                try {
                    ByteBuffer buf = ByteBuffer.allocate(length);
                    while (buf.hasRemaining()) {
                        int n = channel.read(buf, offset + buf.position());
                        if (n <= 0) break;
                    }
                    int got = buf.position();
                    if (got == length) return buf.array();
                    byte[] out = new byte[got];
                    System.arraycopy(buf.array(), 0, out, 0, got);
                    return out;
                } catch (IOException ignored) {
                    // Fall through to stream mode for non-seekable providers.
                }
            }
            try (InputStream in = resolver.openInputStream(uri)) {
                if (in == null) throw new IOException("cannot open stream for " + uri);
                long remaining = offset;
                while (remaining > 0) {
                    long skipped = in.skip(remaining);
                    if (skipped <= 0) {
                        if (in.read() < 0) return new byte[0];
                        remaining--;
                    } else {
                        remaining -= skipped;
                    }
                }
                byte[] buf = new byte[length];
                int got = 0;
                while (got < length) {
                    int n = in.read(buf, got, length - got);
                    if (n <= 0) break;
                    got += n;
                }
                if (got == length) return buf;
                byte[] out = new byte[got];
                System.arraycopy(buf, 0, out, 0, got);
                return out;
            }
        }

        synchronized void close() {
            try { if (channel != null) channel.close(); } catch (IOException ignored) {}
            try { if (stream != null) stream.close(); } catch (IOException ignored) {}
            try { if (pfd != null) pfd.close(); } catch (IOException ignored) {}
        }
    }

    private static final class DirTask {
        final String docId;
        final String prefix;

        DirTask(String docId, String prefix) {
            this.docId = docId;
            this.prefix = prefix;
        }
    }

    static void register(NsgoView view, Supplier<Activity> activitySupplier, Supplier<Context> contextSupplier) {
        view.handle("saf.pick", (data, reply) -> {
            Activity activity = activitySupplier.get();
            if (activity == null) {
                reply.reject("no activity available to pick a folder");
                return;
            }
            if (pendingPick != null) {
                pendingPick.resolve(Map.of("cancelled", true));
                pendingPick = null;
            }
            Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
            intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                    | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION
                    | Intent.FLAG_GRANT_PREFIX_URI_PERMISSION);
            try {
                pendingPick = reply;
                activity.startActivityForResult(intent, PICK_TREE_REQUEST);
            } catch (RuntimeException e) {
                pendingPick = null;
                reply.reject("could not open folder picker: " + e.getMessage());
            }
        });

        view.handle("saf.walk", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            String treeUri = stringArg(data, "uri");
            if (ctx == null || treeUri.isEmpty()) {
                reply.reject("missing tree URI");
                return;
            }
            io.execute(() -> walkTree(ctx, treeUri, reply));
        });

        view.handle("saf.open", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            String treeUri = stringArg(data, "uri");
            String docId = stringArg(data, "docId");
            if (ctx == null || treeUri.isEmpty() || docId.isEmpty()) {
                reply.reject("missing document reference");
                return;
            }
            io.execute(() -> openDoc(ctx, treeUri, docId, reply));
        });

        view.handle("saf.read", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            int handle = (int) longArg(data, "handle", 0);
            long offset = longArg(data, "offset", 0);
            int length = (int) Math.min(MAX_CHUNK_BYTES, Math.max(0, longArg(data, "length", 0)));
            if (ctx == null || handle <= 0) {
                reply.reject("invalid document handle");
                return;
            }
            io.execute(() -> {
                OpenDoc doc = openDocs.get(handle);
                if (doc == null) {
                    reply.reject("document handle closed");
                    return;
                }
                try {
                    byte[] bytes = doc.readAt(ctx.getContentResolver(), offset, length);
                    reply.resolve(Map.of("data", Base64.encodeToString(bytes, Base64.NO_WRAP)));
                } catch (IOException | RuntimeException e) {
                    reply.reject("read failed: " + e.getMessage());
                }
            });
        });

        view.handle("saf.close", (data, reply) -> {
            int handle = (int) longArg(data, "handle", 0);
            OpenDoc doc = handle > 0 ? openDocs.remove(handle) : null;
            if (doc != null) io.execute(doc::close);
            reply.resolve(null);
        });

        view.handle("saf.readFile", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            String treeUri = stringArg(data, "uri");
            String docId = stringArg(data, "docId");
            long requested = longArg(data, "maxBytes", MAX_READ_FILE_BYTES);
            int limit = (int) Math.min(MAX_READ_FILE_BYTES, Math.max(1, requested));
            if (ctx == null || treeUri.isEmpty() || docId.isEmpty()) {
                reply.reject("missing document reference");
                return;
            }
            io.execute(() -> readWholeFile(ctx, treeUri, docId, limit, reply));
        });

        view.handle("saf.stat", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            String treeUri = stringArg(data, "uri");
            String docId = stringArg(data, "docId");
            if (ctx == null || treeUri.isEmpty() || docId.isEmpty()) {
                reply.resolve(Map.of("exists", false));
                return;
            }
            io.execute(() -> statDoc(ctx, treeUri, docId, reply));
        });

        view.handle("saf.release", (data, reply) -> {
            Context ctx = appContext(contextSupplier);
            String treeUri = stringArg(data, "uri");
            if (ctx != null && !treeUri.isEmpty()) {
                io.execute(() -> {
                    try {
                        ctx.getContentResolver().releasePersistableUriPermission(
                                Uri.parse(treeUri), Intent.FLAG_GRANT_READ_URI_PERMISSION);
                    } catch (RuntimeException ignored) {}
                });
            }
            reply.resolve(null);
        });
    }

    static boolean onActivityResult(Context context, int request, int result, Intent data) {
        if (request != PICK_TREE_REQUEST) return false;
        NsgoView.HostReply reply = pendingPick;
        pendingPick = null;
        if (reply == null) return true;
        Uri tree = result == Activity.RESULT_OK && data != null ? data.getData() : null;
        if (tree == null) {
            reply.resolve(Map.of("cancelled", true));
            return true;
        }
        Context appCtx = context.getApplicationContext();
        io.execute(() -> {
            try {
                appCtx.getContentResolver().takePersistableUriPermission(
                        tree, Intent.FLAG_GRANT_READ_URI_PERMISSION);
            } catch (SecurityException e) {
                Log.w(TAG, "takePersistableUriPermission failed for " + tree, e);
                reply.reject("permission: could not keep folder access: " + e.getMessage());
                return;
            }
            String name = treeDisplayName(appCtx, tree);
            reply.resolve(Map.of("uri", tree.toString(), "name", name));
        });
        return true;
    }

    static void closeAll() {
        if (pendingPick != null) {
            pendingPick.resolve(Map.of("cancelled", true));
            pendingPick = null;
        }
        List<OpenDoc> closing = new ArrayList<>();
        for (Integer key : new ArrayList<>(openDocs.keySet())) {
            OpenDoc doc = openDocs.remove(key);
            if (doc != null) closing.add(doc);
        }
        if (!closing.isEmpty()) {
            io.execute(() -> {
                for (OpenDoc doc : closing) doc.close();
            });
        }
    }

    private static void walkTree(Context context, String treeUriString, NsgoView.HostReply reply) {
        Uri tree;
        String rootId;
        try {
            tree = Uri.parse(treeUriString);
            rootId = DocumentsContract.getTreeDocumentId(tree);
        } catch (RuntimeException e) {
            reply.reject("invalid tree URI: " + e.getMessage());
            return;
        }
        ContentResolver resolver = context.getContentResolver();
        String[] columns = {
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                DocumentsContract.Document.COLUMN_MIME_TYPE,
                DocumentsContract.Document.COLUMN_SIZE,
                DocumentsContract.Document.COLUMN_LAST_MODIFIED
        };
        List<Map<String, Object>> entries = new ArrayList<>();
        ArrayDeque<DirTask> queue = new ArrayDeque<>();
        Set<String> visitedDirs = new HashSet<>();
        queue.add(new DirTask(rootId, ""));
        visitedDirs.add(rootId);
        boolean isRoot = true;
        while (!queue.isEmpty()) {
            DirTask dir = queue.poll();
            Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, dir.docId);
            try (Cursor c = resolver.query(children, columns, null, null, null)) {
                if (c == null) {
                    if (isRoot) {
                        reply.reject("permission: folder cannot be read");
                        return;
                    }
                    continue;
                }
                while (c.moveToNext()) {
                    String docId = c.isNull(0) ? "" : c.getString(0);
                    String name = c.isNull(1) ? "" : c.getString(1);
                    String mime = c.isNull(2) ? "" : c.getString(2);
                    long size = c.isNull(3) ? 0 : Math.max(0, c.getLong(3));
                    long mod = c.isNull(4) ? 0 : Math.max(0, c.getLong(4));
                    if (docId.isEmpty() || name.isEmpty() || name.startsWith(".")
                            || name.contains("/") || name.contains("\\")
                            || ".".equals(name) || "..".equals(name)) {
                        continue;
                    }
                    String rel = dir.prefix.isEmpty() ? name : dir.prefix + "/" + name;
                    if (DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                        if (visitedDirs.add(docId)) {
                            queue.add(new DirTask(docId, rel));
                        }
                        continue;
                    }
                    int kind = fileKind(name);
                    if (kind == 0) continue;
                    Map<String, Object> item = new HashMap<>();
                    item.put("rel", rel);
                    item.put("docId", docId);
                    item.put("size", size);
                    item.put("mod", mod);
                    if (kind == 1) {
                        item.put("docUri", DocumentsContract.buildDocumentUriUsingTree(tree, docId).toString());
                    }
                    entries.add(item);
                }
            } catch (SecurityException e) {
                if (isRoot) {
                    reply.reject("permission: " + e.getMessage());
                    return;
                }
            } catch (RuntimeException e) {
                if (isRoot) {
                    reply.reject("could not list folder: " + e.getMessage());
                    return;
                }
            }
            isRoot = false;
        }
        reply.resolve(Map.of("entries", entries));
    }

    /** Returns 1 for audio, 2 for lyrics/cover image, 0 for ignored files. */
    private static int fileKind(String name) {
        int dot = name.lastIndexOf('.');
        if (dot < 0 || dot == name.length() - 1) return 0;
        String ext = name.substring(dot).toLowerCase(Locale.ROOT);
        switch (ext) {
            case ".mp3":
            case ".flac":
            case ".ogg":
            case ".wav":
            case ".m4a":
            case ".aac":
                return 1;
            case ".lrc":
            case ".jpg":
            case ".jpeg":
            case ".png":
                return 2;
            default:
                return 0;
        }
    }

    private static void openDoc(Context context, String treeUri, String docId, NsgoView.HostReply reply) {
        evictStaleDocs();
        Uri uri;
        try {
            uri = DocumentsContract.buildDocumentUriUsingTree(Uri.parse(treeUri), docId);
        } catch (RuntimeException e) {
            reply.reject("invalid document URI: " + e.getMessage());
            return;
        }
        ContentResolver resolver = context.getContentResolver();
        ParcelFileDescriptor pfd = null;
        FileInputStream stream = null;
        FileChannel channel = null;
        long size = -1;
        try {
            pfd = resolver.openFileDescriptor(uri, "r");
            if (pfd != null) {
                long statSize = pfd.getStatSize();
                if (statSize >= 0) size = statSize;
                stream = new FileInputStream(pfd.getFileDescriptor());
                FileChannel ch = stream.getChannel();
                try {
                    long chSize = ch.size();
                    if (chSize >= 0) size = chSize;
                    channel = ch;
                } catch (IOException ignored) {
                    // Pipe/unseekable provider: keep stream fallback.
                }
            }
        } catch (IOException | RuntimeException e) {
            if (pfd != null) {
                try { pfd.close(); } catch (IOException ignored) {}
            }
            reply.reject("open failed: " + e.getMessage());
            return;
        }
        int handle = nextHandle.getAndIncrement();
        openDocs.put(handle, new OpenDoc(uri, pfd, stream, channel, size));
        reply.resolve(Map.of("handle", handle, "size", size));
    }

    private static void evictStaleDocs() {
        long now = System.currentTimeMillis();
        for (Map.Entry<Integer, OpenDoc> e : openDocs.entrySet()) {
            if (now - e.getValue().lastUsedMs > IDLE_DOC_MS) {
                OpenDoc removed = openDocs.remove(e.getKey());
                if (removed != null) removed.close();
            }
        }
        while (openDocs.size() >= MAX_OPEN_DOCS) {
            Integer oldestKey = null;
            long oldestMs = Long.MAX_VALUE;
            for (Map.Entry<Integer, OpenDoc> e : openDocs.entrySet()) {
                if (e.getValue().lastUsedMs < oldestMs) {
                    oldestMs = e.getValue().lastUsedMs;
                    oldestKey = e.getKey();
                }
            }
            if (oldestKey == null) break;
            OpenDoc removed = openDocs.remove(oldestKey);
            if (removed != null) removed.close();
        }
    }

    private static void readWholeFile(Context context, String treeUri, String docId, int limit, NsgoView.HostReply reply) {
        try {
            Uri uri = DocumentsContract.buildDocumentUriUsingTree(Uri.parse(treeUri), docId);
            try (InputStream in = context.getContentResolver().openInputStream(uri)) {
                if (in == null) {
                    reply.reject("cannot open document");
                    return;
                }
                ByteArrayOutputStream out = new ByteArrayOutputStream();
                byte[] buf = new byte[16 * 1024];
                int remaining = limit;
                while (remaining > 0) {
                    int n = in.read(buf, 0, Math.min(buf.length, remaining));
                    if (n <= 0) break;
                    out.write(buf, 0, n);
                    remaining -= n;
                }
                reply.resolve(Map.of("data", Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)));
            }
        } catch (IOException | RuntimeException e) {
            reply.reject("readFile failed: " + e.getMessage());
        }
    }

    private static void statDoc(Context context, String treeUri, String docId, NsgoView.HostReply reply) {
        try {
            Uri uri = DocumentsContract.buildDocumentUriUsingTree(Uri.parse(treeUri), docId);
            String[] columns = {DocumentsContract.Document.COLUMN_SIZE};
            try (Cursor c = context.getContentResolver().query(uri, columns, null, null, null)) {
                if (c != null && c.moveToFirst()) {
                    long size = c.isNull(0) ? 0 : Math.max(0, c.getLong(0));
                    reply.resolve(Map.of("exists", true, "size", size));
                    return;
                }
            }
        } catch (RuntimeException ignored) {}
        reply.resolve(Map.of("exists", false));
    }

    private static String treeDisplayName(Context context, Uri tree) {
        try {
            String treeId = DocumentsContract.getTreeDocumentId(tree);
            if (treeId != null) {
                int colon = treeId.indexOf(':');
                if (colon >= 0) {
                    String vol = treeId.substring(0, colon);
                    String rel = treeId.substring(colon + 1);
                    while (rel.startsWith("/")) rel = rel.substring(1);
                    while (rel.endsWith("/")) rel = rel.substring(0, rel.length() - 1);
                    if (!rel.isEmpty()) {
                        return "primary".equalsIgnoreCase(vol) ? rel : (vol.isEmpty() ? rel : vol + "/" + rel);
                    }
                }
            }
            Uri doc = DocumentsContract.buildDocumentUriUsingTree(tree, treeId);
            String[] cols = {DocumentsContract.Document.COLUMN_DISPLAY_NAME};
            try (Cursor c = context.getContentResolver().query(doc, cols, null, null, null)) {
                if (c != null && c.moveToFirst() && !c.isNull(0)) {
                    String name = c.getString(0);
                    if (name != null && !name.isEmpty()) return name;
                }
            }
        } catch (RuntimeException ignored) {}
        String last = tree.getLastPathSegment();
        return last != null && !last.isEmpty() ? last : tree.toString();
    }

    private static Context appContext(Supplier<Context> supplier) {
        Context c = supplier.get();
        return c != null ? c.getApplicationContext() : null;
    }

    @SuppressWarnings("unchecked")
    private static String stringArg(Object data, String key) {
        if (!(data instanceof Map)) return "";
        Object v = ((Map<String, Object>) data).get(key);
        return v instanceof String ? (String) v : "";
    }

    @SuppressWarnings("unchecked")
    private static long longArg(Object data, String key, long fallback) {
        if (!(data instanceof Map)) return fallback;
        Object v = ((Map<String, Object>) data).get(key);
        return v instanceof Number ? ((Number) v).longValue() : fallback;
    }
}
