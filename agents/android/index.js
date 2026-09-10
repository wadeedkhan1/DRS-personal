/**
 * @format
 */

import {AppRegistry} from 'react-native';
import App from './App';
import {name as appName} from './app.json';
import {bootRuntime} from './src/runtime';

AppRegistry.registerComponent(appName, () => App);

// Start the agent with the bundle, not with the UI. This runs whenever the JS context is
// created — including headlessly, when ConnectionService recreates it after the activity
// was destroyed — so the connection does not depend on a screen being on, or on the app
// still being in recents. It only reconnects if the agent was connected when it last shut
// down; a deliberate Disconnect stays disconnected.
bootRuntime();
