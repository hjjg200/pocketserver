const esbuild = require('esbuild');
const fs = require('fs');
const path = require('path');

const copyFiles = (srcDir, destDir, prefix = "") => {
    if (!fs.existsSync(destDir)) {
        fs.mkdirSync(destDir, { recursive: true });
    }

    const files = fs.readdirSync(srcDir);
    for (const file of files) {
        const srcFile = path.join(srcDir, file);

        if (fs.statSync(srcFile).isDirectory()) {
            const destSubDir = path.join(destDir, file); // Keep the same directory name
            copyFiles(srcFile, destSubDir, prefix); // Recursively copy directories
        } else {
            // Add the prefix to the destination file's basename
            const destFile = path.join(destDir, prefix + file);
            fs.copyFileSync(srcFile, destFile);
            console.log(`Copied: ${srcFile} -> ${destFile}`);
        }
    }
};


esbuild.build({
    entryPoints: ['src/main.js'],  // Path to your main JavaScript file
    outfile: 'dist/static/ffmpeg_pipe.js', // Output bundled file
    bundle: true,                // Bundle all dependencies
    format: 'esm',               // Ensure ES module output
    platform: 'browser',
}).then(() => {
    console.log('Build successful!');
    
    // Copy @ffmpeg/core/dist/esm/* to static/ffmpeg/*
    /*
    copyFiles(
        path.resolve('node_modules/@ffmpeg/core/dist/esm'),
        path.resolve('dist/static/ffmpeg')
    );*/

    // Copy @ffmpeg/core-mt/dist/esm/* to static/ffmpeg/*
    copyFiles(
        path.resolve('node_modules/@ffmpeg/core-mt/dist/esm'),
        path.resolve('dist/static/ffmpeg'),
        "mt-"
    );

    // Copy @ffmpeg/ffmpeg/dist/esm/* to static/ffmpeg/*
    copyFiles(
        path.resolve('node_modules/@ffmpeg/ffmpeg/dist/esm'),
        path.resolve('dist/static/ffmpeg')
    );

    console.log('File copying completed!');
}).catch((error) => {
    console.error('Build failed:', error);
});
