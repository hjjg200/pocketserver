const esbuild = require('esbuild');
const fs = require('fs');
const path = require('path');

const copyFiles = (srcDir, destDir) => {
    if (!fs.existsSync(destDir)) {
        fs.mkdirSync(destDir, { recursive: true });
    }

    const files = fs.readdirSync(srcDir);
    for (const file of files) {
        const srcFile = path.join(srcDir, file);
        const destFile = path.join(destDir, file);

        if (fs.statSync(srcFile).isDirectory()) {
            copyFiles(srcFile, destFile); // Recursively copy directories
        } else {
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
    copyFiles(
        path.resolve('node_modules/@ffmpeg/core/dist/esm'),
        path.resolve('dist/static/ffmpeg')
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
