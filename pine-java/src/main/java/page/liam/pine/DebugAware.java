package page.liam.pine;

public interface DebugAware {
    void setDebug(String operatorName, boolean debug);
    boolean isDebug();
}
