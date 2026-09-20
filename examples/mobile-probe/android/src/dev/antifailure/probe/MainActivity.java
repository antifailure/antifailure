package dev.antifailure.probe;

import android.app.Activity;
import android.os.Bundle;
import android.view.Gravity;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

/** The Android half of the mobile probe app.
 *
 * Deliberately the same three controls as the iOS half, with the same
 * accessible names, so one workflow sentence drives both surfaces and any
 * difference in the verdict is a difference in the driver rather than in the
 * application. Every control carries an explicit content description, because
 * the content description is what a screen reader reads and therefore what the
 * accessibility snapshot is built from.
 */
public class MainActivity extends Activity {
  @Override
  protected void onCreate(Bundle state) {
    super.onCreate(state);
    LinearLayout root = new LinearLayout(this);
    root.setOrientation(LinearLayout.VERTICAL);
    root.setGravity(Gravity.CENTER);
    root.setPadding(48, 48, 48, 48);

    final EditText email = new EditText(this);
    email.setHint("Email");
    email.setContentDescription("Email");
    email.setSingleLine(true);
    root.addView(email, new LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

    final TextView greeting = new TextView(this);
    greeting.setText("");
    greeting.setContentDescription("");
    greeting.setGravity(Gravity.CENTER);

    Button signIn = new Button(this);
    signIn.setText("Sign In");
    signIn.setContentDescription("Sign In");
    signIn.setOnClickListener(v -> {
      // The signed in state is the whole point of the probe: it is what an
      // expectation can be met by, and what a deliberately wrong expectation
      // must fail against.
      greeting.setText("Welcome back");
      greeting.setContentDescription("Welcome back");
    });
    root.addView(signIn, new LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

    // The failure path, matching the iOS half. A driver that can only show a
    // workflow passing has proved half of what matters: the job is to say no
    // about an application that is broken. "Something went wrong" is one of
    // the sentences runner/src/workflow.ts recognises as a screen showing a
    // failure instead of a result, which is what turns an unmatched
    // expectation from "nothing was proved" into "this did not work".
    Button deleteAccount = new Button(this);
    deleteAccount.setText("Delete Account");
    deleteAccount.setContentDescription("Delete Account");
    deleteAccount.setOnClickListener(v -> {
      greeting.setText("Something went wrong. Please try again.");
      greeting.setContentDescription("Something went wrong. Please try again.");
    });
    root.addView(deleteAccount, new LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

    root.addView(greeting, new LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

    setContentView(root);
  }
}
