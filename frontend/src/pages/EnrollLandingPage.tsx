import React, { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Shield, Copy, Check, Monitor, Smartphone, AlertCircle, Download, Zap } from 'lucide-react';
import { useAgentDownload, androidSetupLink } from '../hooks/useAgentDownload';

/**
 * Where an invite link lands when somebody clicks it.
 *
 * This page still does not enroll anything itself — enrollment is the agent posting to
 * /api/devices/enroll from the machine being enrolled, sending its real hostname and OS
 * and receiving a device secret it stores locally. A browser cannot do that.
 *
 * What changed is that the recipient no longer has to carry anything across. The Windows
 * download is personalised per invite: the backend appends this server's address and this
 * token to the binary, so running it is the whole procedure. Android cannot be
 * personalised the same way — the system renames an installed APK to base.apk and an app
 * cannot read its own installer — so the phone gets a `drs://` link instead, which is
 * what the "Set up the agent" button fires.
 *
 * The code is still shown, in a disclosure, for the case this page cannot serve: an
 * agent that is already installed, or one obtained some other way.
 *
 * It is public because whoever installs an agent generally has no portal account.
 */
export const EnrollLandingPage: React.FC = () => {
  const [params] = useSearchParams();
  const token = params.get('token');
  const [copied, setCopied] = useState(false);

  const windowsAgent = useAgentDownload('windows', token ?? undefined);
  const androidAgent = useAgentDownload('android', token ?? undefined);

  // Rough, and deliberately so: it only decides which platform's instructions to lead
  // with, and both remain reachable either way.
  const looksAndroid = /android/i.test(navigator.userAgent);
  const setupLink = token ? androidSetupLink(window.location.origin, token) : '';

  const copy = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // Both probes have answered and neither binary is there. Distinguished from "still
  // checking" so the page does not flash an error while the requests are in flight.
  const nothingToDownload =
    windowsAgent.available === false && androidAgent.available === false;

  const stepLabel = (n: number, text: string) => (
    <div className="flex items-center gap-2.5 mb-3">
      <span className="flex items-center justify-center w-5 h-5 rounded-full bg-sky-500/15 border border-sky-500/30 text-[10px] font-bold text-sky-300 shrink-0">
        {n}
      </span>
      <p className="text-xs font-semibold text-slate-300 uppercase tracking-wider">{text}</p>
    </div>
  );

  return (
    <div className="min-h-screen w-full flex items-center justify-center p-6 bg-[radial-gradient(ellipse_at_top,_var(--tw-gradient-stops))] from-slate-900 via-slate-950 to-black">
      <div className="w-full max-w-lg">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-gradient-to-br from-sky-500 to-blue-700 shadow-xl shadow-sky-500/25 mb-4">
            <Shield className="w-7 h-7 text-white" />
          </div>
          <h1 className="text-2xl font-bold text-slate-100 tracking-tight">Enroll this device</h1>
          <p className="text-sm text-slate-400 mt-1">
            Connect this machine to the DRS monitoring platform.
          </p>
        </div>

        <div className="rounded-2xl bg-slate-900/80 backdrop-blur-xl border border-slate-800 p-8 shadow-2xl">
          {!token ? (
            <div className="flex items-start gap-3 text-xs text-amber-400">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
              <div className="space-y-1.5">
                <p className="font-semibold">No enrollment token in this link.</p>
                <p className="text-slate-400 leading-relaxed">
                  An invite link looks like <span className="font-mono">/enroll?token=DRS-…</span>.
                  Ask your administrator to send you a new one.
                </p>
              </div>
            </div>
          ) : (
            <>
              {/* Step 1 — the software, which for Windows is also the configuration. */}
              <div className="mb-7">
                {stepLabel(1, 'Download the agent')}

                <div className="space-y-2">
                  {/* Android first on a phone: the order the person will actually use. */}
                  {looksAndroid && androidAgent.available && (
                    <a
                      href={androidAgent.url}
                      className="flex items-center gap-3 p-3.5 rounded-xl bg-slate-800/60 hover:bg-slate-800 border border-slate-700/60 hover:border-emerald-500/50 transition-all group"
                    >
                      <Smartphone className="w-4 h-4 text-emerald-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-semibold text-slate-200">Android agent</div>
                        <div className="text-[11px] text-slate-500">
                          Android 8.0+ · drs-agent.apk
                        </div>
                      </div>
                      <Download className="w-4 h-4 text-slate-500 group-hover:text-emerald-400 shrink-0 transition-colors" />
                    </a>
                  )}

                  {windowsAgent.available && (
                    <a
                      href={windowsAgent.url}
                      className="flex items-center gap-3 p-3.5 rounded-xl bg-slate-800/60 hover:bg-slate-800 border border-slate-700/60 hover:border-sky-500/50 transition-all group"
                    >
                      <Monitor className="w-4 h-4 text-sky-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-semibold text-slate-200">
                          Windows agent
                        </div>
                        <div className="text-[11px] text-slate-500">
                          Windows 10, 11 and Server · already configured
                        </div>
                      </div>
                      <Download className="w-4 h-4 text-slate-500 group-hover:text-sky-400 shrink-0 transition-colors" />
                    </a>
                  )}

                  {!looksAndroid && androidAgent.available && (
                    <a
                      href={androidAgent.url}
                      className="flex items-center gap-3 p-3.5 rounded-xl bg-slate-800/60 hover:bg-slate-800 border border-slate-700/60 hover:border-emerald-500/50 transition-all group"
                    >
                      <Smartphone className="w-4 h-4 text-emerald-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="text-sm font-semibold text-slate-200">Android agent</div>
                        <div className="text-[11px] text-slate-500">
                          Android 8.0+ · drs-agent.apk
                        </div>
                      </div>
                      <Download className="w-4 h-4 text-slate-500 group-hover:text-emerald-400 shrink-0 transition-colors" />
                    </a>
                  )}

                  {nothingToDownload && (
                    <div className="p-3.5 rounded-xl bg-slate-800/40 border border-slate-700/60 flex items-start gap-2.5">
                      <AlertCircle className="w-3.5 h-3.5 text-slate-500 mt-0.5 shrink-0" />
                      <p className="text-[11px] text-slate-400 leading-relaxed">
                        No agent is published on this server yet. Your administrator will need
                        to send you the DRS Agent directly — your code below still works once
                        you have it.
                      </p>
                    </div>
                  )}
                </div>

                {(windowsAgent.available || androidAgent.available) && (
                  <p className="text-[11px] text-slate-500 mt-2.5 leading-relaxed">
                    Windows may warn that the publisher is unrecognised — the agent is not
                    code-signed. Choose <span className="text-slate-400">More info</span> then{' '}
                    <span className="text-slate-400">Run anyway</span>.
                  </p>
                )}
              </div>

              {/* Step 2 — what to do with it. Short, because on Windows there is
                  nothing to do beyond running the file. */}
              <div className="space-y-4 pt-6 border-t border-slate-800">
                {stepLabel(2, 'Run it')}

                <div className="flex items-start gap-3">
                  <Monitor className="w-4 h-4 text-sky-400 mt-0.5 shrink-0" />
                  <div className="text-xs text-slate-400 leading-relaxed">
                    <span className="text-slate-200 font-semibold">Windows —</span> run the
                    file you just downloaded on the PC you want monitored. That is the whole
                    procedure: it already knows this server and this invite, connects itself,
                    and starts with the PC from then on.
                  </div>
                </div>

                <div className="flex items-start gap-3">
                  <Smartphone className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
                  <div className="text-xs text-slate-400 leading-relaxed">
                    <span className="text-slate-200 font-semibold">Android —</span> install the
                    APK, then tap the button below. Approve the screen-capture prompt when an
                    operator starts a session.
                  </div>
                </div>

                {/* The phone's equivalent of the configured .exe. Always offered, not
                    only on Android: someone may be reading this page on a laptop and
                    forwarding the link to the handset. */}
                <a
                  href={setupLink}
                  className="flex items-center gap-3 p-3.5 rounded-xl bg-emerald-500/10 hover:bg-emerald-500/20 border border-emerald-500/30 transition-all"
                >
                  <Zap className="w-4 h-4 text-emerald-400 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="text-sm font-semibold text-emerald-300">
                      Set up the agent
                    </div>
                    <div className="text-[11px] text-slate-400">
                      Android only · opens the installed app and enrolls it
                    </div>
                  </div>
                </a>

                <div className="p-3.5 rounded-xl bg-amber-500/[0.06] border border-amber-500/20 flex items-start gap-2.5">
                  <AlertCircle className="w-3.5 h-3.5 text-amber-400 mt-0.5 shrink-0" />
                  <p className="text-[11px] text-slate-400 leading-relaxed">
                    Run the agent on the device you want monitored — opening this page in a
                    browser does not enroll anything. Once it runs, an authorised operator
                    can view that device's screen. Treat this link like a password: anyone
                    who opens it can enroll a device into this organization.
                  </p>
                </div>

                {/* The manual route, for an agent that did not come from this page. */}
                <details className="text-[11px] text-slate-500">
                  <summary className="cursor-pointer text-slate-400 hover:text-slate-200">
                    Already have the agent installed?
                  </summary>
                  <div className="mt-2 space-y-2">
                    <p className="leading-relaxed">
                      Enter this server address and code into it by hand.
                    </p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 px-3 py-2.5 rounded-xl bg-slate-950/80 border border-slate-800 text-xs font-mono text-slate-300 break-all">
                        {window.location.origin}
                      </code>
                    </div>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 px-3 py-2.5 rounded-xl bg-slate-950/80 border border-slate-800 text-xs font-mono text-sky-300 break-all">
                        {token}
                      </code>
                      <button
                        onClick={() => copy(token)}
                        title="Copy code"
                        className="p-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700/60 transition-colors shrink-0"
                      >
                        {copied ? (
                          <Check className="w-4 h-4 text-emerald-400" />
                        ) : (
                          <Copy className="w-4 h-4" />
                        )}
                      </button>
                    </div>
                  </div>
                </details>
              </div>
            </>
          )}

          <div className="mt-6 pt-6 border-t border-slate-800 text-center">
            <Link to="/login" className="text-xs text-sky-400 hover:text-sky-300">
              Administrator sign-in
            </Link>
          </div>
        </div>
      </div>
    </div>
  );
};
