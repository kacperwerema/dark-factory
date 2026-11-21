import {Config} from '@remotion/cli/config';

// The kit encodes the final MP4 itself (loudness, AAC, faststart): this
// render only makes the frames, so a fast, high-quality intermediate.
Config.setVideoImageFormat('jpeg');
Config.setCodec('h264');
Config.setCrf(16);
Config.setOverwriteOutput(true);
